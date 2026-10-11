package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
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
			if errors.Is(err, runner.ErrContainerCleanup) {
				return err
			}
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
