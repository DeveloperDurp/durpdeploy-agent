package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
)

const claimFileName = "current-claim.json"

const reconnectDelay = time.Second

var newExecutor = runner.NewExecutor

const agentHelpText = `Usage: durpdeploy-agent

Starts a local pairing listener until paired, then polls the persisted server.

Inputs:
  DURPDEPLOY_AGENT_LISTEN_ADDR  local address used only while pairing
  DURPDEPLOY_AGENT_STATE_DIR    private persistent state directory
  DURPDEPLOY_AGENT_VERSION      agent version sent after pairing
  DURPDEPLOY_AGENT_CONTAINER_ENABLED  enable agent/3 container execution (false)
  DURPDEPLOY_AGENT_CONTAINER_RUNTIME  docker or podman (required when enabled)
  DURPDEPLOY_AGENT_CONTAINER_SOCKET   absolute unix socket URL (required when enabled)

Pairing stores the server URL, pinned fingerprints, and agent ID in state.
Do not provide server connection settings manually.
`

type claimMarker struct {
	DeploymentID         int64                       `json:"deployment_id"`
	TokenHash            string                      `json:"token_hash"`
	CleanupResult        json.RawMessage             `json:"cleanup_result,omitempty"`
	Runtime              agentproto.ContainerRuntime `json:"runtime,omitempty"`
	SocketURL            string                      `json:"socket_url,omitempty"`
	CleanupAcknowledged  bool                        `json:"cleanup_acknowledged,omitempty"`
	TerminalReport       json.RawMessage             `json:"terminal_report,omitempty"`
	TerminalAcknowledged bool                        `json:"terminal_acknowledged,omitempty"`
}

type claimPhase uint8

const (
	claimLegacy claimPhase = iota
	claimCleanupPending
	claimCleanupAcknowledged
)

func main() {
	if len(os.Args) == 2 &&
		(os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h") {
		fmt.Fprint(os.Stdout, agentHelp())
		return
	}
	configuration, err := loadConfig()
	if err != nil {
		slog.Error("invalid agent configuration", "err", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	if err := run(
		ctx,
		configuration,
	); err != nil &&
		!errors.Is(err, context.Canceled) {
		slog.Error("agent stopped", "err", err)
		os.Exit(1)
	}
}

func agentHelp() string {
	return agentHelpText
}

func run(ctx context.Context, configuration config) error {
	lease, err := acquireStateLease(configuration.stateDir)
	if err != nil {
		return err
	}
	defer func() {
		if err := lease.Close(); err != nil {
			slog.Error("release agent state lease", "err", err)
		}
	}()
	for ctx.Err() == nil {
		client, err := agentclient.NewPaired(
			configuration.stateDir,
			configuration.agentVersion,
		)
		if errors.Is(err, agentstate.ErrRePairRequired) {
			err = runBootstrap(ctx, configuration.bootstrap)
		} else if err == nil {
			if configuration.containers {
				err = client.EnableContainers(ctx, configuration.container)
			}
			if err == nil {
				err = runPaired(ctx, client)
			}
			client.Close()
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			slog.Warn(
				"agent operation failed; retrying",
				"err", err,
				"retry_in", reconnectDelay,
			)
			if !waitForRetry(ctx) {
				break
			}
			continue
		}
	}
	return ctx.Err()
}

func runPaired(ctx context.Context, client *agentclient.Client) error {
	for ctx.Err() == nil {
		if err := resumeCleanupResult(ctx, client); err != nil {
			return err
		}
		claim, err := client.Poll(ctx)
		if err != nil {
			return err
		}
		if claim == nil {
			continue
		}
		if err := executeClaim(ctx, client, *claim); err != nil {
			var statusErr *agentclient.StatusError
			if errors.As(err, &statusErr) && statusErr.Status == 409 {
				continue
			}
			return err
		}
	}
	return ctx.Err()
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(reconnectDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func executeClaim(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) error {
	if err := persistClaim(client, claim, claimLegacy); err != nil {
		return err
	}
	clearMarker := true
	defer func() {
		if clearMarker {
			if clearErr := clearClaim(client); clearErr != nil {
				slog.Error("remove claim marker", "err", clearErr)
			}
		}
	}()
	plaintext, err := client.DecodePayload(claim)
	if err != nil {
		return err
	}
	payload, err := decodePayload(plaintext, int64(claim.DeploymentID))
	if err != nil {
		return err
	}
	if err := payload.validateExecution(client.SupportsStep); err != nil {
		return err
	}
	if err := client.ValidateExecutionReady(ctx); err != nil {
		return err
	}
	slog.Info(
		"deployment received",
		"deployment_id", claim.DeploymentID,
		"steps", len(payload.Release.Steps),
	)
	if err := client.Start(
		ctx,
		claim.DeploymentID,
		claim.ClaimToken,
	); err != nil {
		return err
	}
	slog.Info("deployment started", "deployment_id", claim.DeploymentID)
	for _, step := range payload.Release.Steps {
		if step.ExecutionMode == agentproto.ExecutionContainer {
			if err := persistClaim(
				client,
				claim,
				claimCleanupPending,
			); err != nil {
				// Start was acknowledged, but no workload has started. Retire any
				// partially written recovery report before sending a normal failure.
				clearMarker = false
				clearErr := clearClaim(client)
				result := agentproto.ResultFailed
				if clearErr != nil {
					result = agentproto.ResultCleanupUnconfirmed
				}
				reportCtx, reportCancel := context.WithTimeout(
					context.WithoutCancel(
						ctx,
					),
					agentproto.CancelAcknowledgementTimeout,
				)
				defer reportCancel()
				return errors.Join(err, clearErr, client.Result(reportCtx,
					claim.DeploymentID, agentproto.ResultRequest{
						ClaimToken: claim.ClaimToken,
						State:      result,
						Error:      "No workload was started. Claim recovery state could not be persisted; restore access to the agent state directory.",
					}))
			}
			break
		}
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelled := false
	var cancelMu sync.Mutex
	logs := newLogSender(executionCtx, client, claim)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(agentproto.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				response, heartbeatErr := client.Heartbeat(
					executionCtx,
					claim.DeploymentID,
					claim.ClaimToken,
				)
				if heartbeatErr != nil {
					cancel()
					return
				}
				if response.CancelRequested {
					cancelMu.Lock()
					cancelled = true
					cancelMu.Unlock()
					cancel()
					return
				}
			}
		}
	}()

	environment, secrets, err := payload.environment()
	if err == nil {
		executor := newExecutor()
		if client.ContainerExecutor() != nil {
			executor = runner.NewExecutorWithContainers(
				client.ContainerExecutor(),
			)
		}
		err = executor.ExecuteSteps(executionCtx, runner.ExecutionConfig{
			DeploymentID: int64(claim.DeploymentID),
			Steps:        payload.Release.Steps,
			Environment:  environment,
			Secrets:      secrets,
			CallbacksForStep: func(step runner.Step) runner.Callbacks {
				slog.Info(
					"step started",
					"deployment_id", claim.DeploymentID,
					"step", step.Name,
				)
				return runner.NewCallbacks(runner.CallbacksConfig{
					WriteLog: func(line string) error {
						slog.Info(
							"step output",
							"deployment_id", claim.DeploymentID,
							"step", step.Name,
							"output", line,
						)
						return logs.Write(line)
					},
					Cancelled: func() bool {
						cancelMu.Lock()
						defer cancelMu.Unlock()
						return cancelled
					},
				})
			},
			StepFinished: func(step runner.Step, stepErr error) {
				status := agentproto.ResultSucceeded
				if stepErr != nil {
					status = agentproto.ResultFailed
				}
				slog.Info(
					"step finished",
					"deployment_id", claim.DeploymentID,
					"step", step.Name,
					"status", status,
				)
			},
		})
	}
	cancel()
	<-heartbeatDone
	if flushErr := logs.Flush(ctx); flushErr != nil && err == nil {
		err = flushErr
	}
	cancelMu.Lock()
	wasCancelled := cancelled || errors.Is(err, runner.ErrCancelled) ||
		ctx.Err() != nil
	cancelMu.Unlock()
	if errors.Is(err, runner.ErrContainerCleanup) {
		clearMarker = false
		reportCtx, reportCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			agentproto.CancelAcknowledgementTimeout,
		)
		defer reportCancel()
		if reportErr := client.Result(
			reportCtx,
			claim.DeploymentID,
			agentproto.ResultRequest{
				ClaimToken: claim.ClaimToken,
				State:      agentproto.ResultCleanupUnconfirmed,
				Error:      "Container cleanup could not be confirmed. Restore runtime access; the agent will reconcile its owned attempts before polling.",
			},
		); reportErr != nil {
			return reportErr
		}
		return errors.Join(
			err,
			persistClaim(client, claim, claimCleanupAcknowledged),
		)
	}
	// Replace conservative cleanup state with the actual outcome before upload.
	// A failed upload must retain the claim authentication for restart recovery.
	clearMarker = false
	if wasCancelled {
		slog.Info(
			"deployment execution finished",
			"deployment_id", claim.DeploymentID,
			"status", "cancelled",
		)
		ackCtx, ackCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			agentproto.CancelAcknowledgementTimeout,
		)
		defer ackCancel()
		if persistErr := persistTerminalResult(
			client,
			claim,
			agentproto.CancelledRequest{
				ClaimToken: claim.ClaimToken,
			},
		); persistErr != nil {
			return persistErr
		}
		return resumeCleanupResult(ackCtx, client)
	}
	result := agentproto.ResultSucceeded
	if err != nil {
		result = agentproto.ResultFailed
	}
	slog.Info(
		"deployment execution finished",
		"deployment_id", claim.DeploymentID,
		"status", result,
	)
	if persistErr := persistTerminalResult(
		client,
		claim,
		agentproto.ResultRequest{
			ClaimToken: claim.ClaimToken, State: result,
		},
	); persistErr != nil {
		return errors.Join(err, persistErr)
	}
	return resumeCleanupResult(ctx, client)
}

func persistTerminalResult(
	client *agentclient.Client,
	claim agentproto.PollResponse,
	request agentproto.Request,
) error {
	sealed, err := client.SealTerminalReport(claim.DeploymentID, request)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	return writeClaimMarker(client, claimMarker{
		DeploymentID: int64(
			claim.DeploymentID,
		),
		TokenHash:      fmt.Sprintf("%x", digest),
		TerminalReport: sealed,
	})
}

func persistClaim(
	client *agentclient.Client,
	claim agentproto.PollResponse,
	phase claimPhase,
) error {
	// Legacy markers retain only a hash. Started container claims also retain an
	// encrypted terminal report before any workload can begin.
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	marker := claimMarker{
		DeploymentID: int64(claim.DeploymentID),
		TokenHash:    fmt.Sprintf("%x", digest),
	}
	switch phase {
	case claimLegacy:
	case claimCleanupPending, claimCleanupAcknowledged:
		sealed, err := client.SealCleanupReport(claim)
		if err != nil {
			return err
		}
		marker.CleanupResult = sealed
		marker.Runtime = client.ContainerExecutor().Runtime()
		marker.SocketURL = client.ContainerExecutor().SocketURL()
		marker.CleanupAcknowledged = phase == claimCleanupAcknowledged
	}
	return writeClaimMarker(client, marker)
}

func writeClaimMarker(client *agentclient.Client, marker claimMarker) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	path := filepath.Join(client.StateDir(), claimFileName)
	return writePrivateFile(path, contents)
}

func resumeCleanupResult(
	ctx context.Context,
	client *agentclient.Client,
) error {
	path := filepath.Join(client.StateDir(), claimFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending claim: %w", err)
	}
	var marker claimMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return fmt.Errorf("decode pending claim: %w", err)
	}
	id := agentproto.DeploymentID(marker.DeploymentID)
	if len(marker.TerminalReport) > 0 {
		if len(marker.CleanupResult) > 0 {
			return fmt.Errorf(
				"pending claim contains conflicting recovery reports",
			)
		}
		request, err := client.DecodeTerminalReport(id, marker.TerminalReport)
		if err != nil {
			return err
		}
		if !marker.TerminalAcknowledged {
			if err := client.ReportTerminal(ctx, id, request); err != nil {
				return err
			}
			marker.TerminalAcknowledged = true
			if err := writeClaimMarker(client, marker); err != nil {
				return err
			}
		}
		return clearClaim(client)
	}
	if len(marker.CleanupResult) == 0 {
		return nil
	}
	result, err := client.DecodeCleanupReport(id, marker.CleanupResult)
	if err != nil {
		return err
	}
	if client.ContainerExecutor() == nil {
		return fmt.Errorf(
			"restore container configuration to reconcile pending claim: %w",
			runner.ErrContainerCleanup,
		)
	}
	if marker.Runtime != client.ContainerExecutor().Runtime() ||
		marker.SocketURL != client.ContainerExecutor().SocketURL() {
		return fmt.Errorf(
			"restore original runtime and socket to reconcile pending claim: %w",
			runner.ErrContainerCleanup,
		)
	}
	if err := client.ValidateExecutionReady(ctx); err != nil {
		return err
	}
	if !marker.CleanupAcknowledged {
		if err := client.Result(ctx, id, result); err != nil {
			return err
		}
		marker.CleanupAcknowledged = true
		if err := writeClaimMarker(client, marker); err != nil {
			return err
		}
	}
	return clearClaim(client)
}

func clearClaim(client *agentclient.Client) error {
	if err := os.Remove(
		filepath.Join(client.StateDir(), claimFileName),
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncClaimDirectory(client.StateDir())
}

func writePrivateFile(path string, contents []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			temporaryPath,
		); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) &&
			err == nil {
			err = removeErr
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncClaimDirectory(filepath.Dir(path))
}

func syncClaimDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
