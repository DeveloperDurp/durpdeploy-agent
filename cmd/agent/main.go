package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/buildinfo"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
)

const claimFileName = "current-claim.json"

const reconnectDelay = time.Second

var newExecutor = executorForClient

const agentHelpText = `Usage: durpdeploy-agent [version|--version|-v|help]

Starts a local pairing listener until paired, then polls the persisted server.

Inputs:
  DURPDEPLOY_AGENT_CONFIG_FILE  optional complete YAML file; environment overrides YAML
  DURPDEPLOY_AGENT_LISTEN_ADDR  local address used only while pairing
  DURPDEPLOY_AGENT_STATE_DIR    private persistent state directory
  DURPDEPLOY_AGENT_VERSION      optional reported version override (defaults to build identity)
  DURPDEPLOY_AGENT_HOSTNAME     optional physical hostname for execution logs
  DURPDEPLOY_AGENT_EXECUTION_BOUNDARY  service execution boundary required for host jobs
  DURPDEPLOY_EXTRA_SCRUB_PATTERNS  optional comma-separated log redaction patterns
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
	HostStagingCleanup   bool                        `json:"host_staging_cleanup,omitempty"`
	HostStagingRoots     []*stagingRootRecord        `json:"host_staging_roots,omitempty"`
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
	if status, handled := runner.HostSupervisorMain(); handled {
		os.Exit(status)
	}
	if writeAgentDiagnostic(os.Args[1:], os.Stdout) {
		return
	}
	configuration, err := loadConfig()
	if err != nil {
		slog.Error("invalid agent configuration", "err", err)
		os.Exit(1)
	}
	slog.Info("agent build", "build", buildinfo.Current().String(),
		"reported_version", configuration.agentVersion)
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

func writeAgentDiagnostic(arguments []string, output io.Writer) bool {
	if len(arguments) != 1 {
		return false
	}
	var err error
	switch arguments[0] {
	case "help", "--help", "-h":
		_, err = fmt.Fprint(output, agentHelp())
	case "version", "--version", "-v":
		_, err = fmt.Fprintln(output, "durpdeploy-agent "+buildinfo.Current().String())
		if override := os.Getenv("DURPDEPLOY_AGENT_VERSION"); override != "" && err == nil {
			_, err = fmt.Fprintf(output, "Reported agent version override: %q\n", override)
		}
	default:
		return false
	}
	if err != nil {
		slog.Error("write agent diagnostic", "err", err)
	}
	return true
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
		client, err := agentclient.NewPairedWithResourcePolicy(
			configuration.stateDir,
			configuration.agentVersion,
			configuration.execution,
			configuration.limits,
		)
		if errors.Is(err, agentstate.ErrRePairRequired) {
			err = runBootstrap(ctx, configuration.bootstrap)
		} else if err == nil {
			err = runConfiguredClient(ctx, client, configuration)
		}
		if errors.Is(err, runner.ErrHostProcessCleanup) {
			return err
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

func runConfiguredClient(ctx context.Context, client *agentclient.Client, configuration config) error {
	var err error
	if configuration.containers {
		err = client.EnableContainersWithPolicy(ctx, configuration.container, configuration.limits.Container)
	}
	if err == nil {
		err = runPaired(ctx, client)
	}
	client.Close()
	return err
}

func runPaired(ctx context.Context, client *agentclient.Client) error {
	stagingReady := false
	for ctx.Err() == nil {
		if err := resumeCleanupResult(ctx, client); err != nil {
			return err
		}
		if !stagingReady {
			if err := client.ResetStaging(); err != nil {
				return err
			}
			stagingReady = true
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
