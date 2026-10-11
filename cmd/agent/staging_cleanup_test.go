package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestRunPaired_staging_cleanup_conflict_stops_polling_until_recovery(
	t *testing.T,
) {
	for _, accepted := range []bool{false, true} {
		t.Run(map[bool]string{false: "conflict", true: "accepted"}[accepted],
			func(t *testing.T) { testHostStagingCleanup(t, accepted) })
	}
}

func testHostStagingCleanup(
	t *testing.T, accepted bool,
) {
	if os.Geteuid() == 0 {
		t.Skip("permission failure requires a non-root agent")
	}
	fixture := newAgentSubprocessFixture(
		t,
		`printf retained > "$DURPDEPLOY_STAGE_DIR/data"; stage_parent="$(dirname "$DURPDEPLOY_STAGE_DIR")"; if [ "$(basename "$stage_parent")" != staging ]; then stage_parent="$(dirname "$stage_parent")"; fi; chmod 000 "$stage_parent"; exit 7`,
	)
	client, _ := claimLifecycleClient(t, fixture)
	client.Close()
	client, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	fixture.payload.Release.Staging = true
	fixture.payload.Release.Steps[0].ExecutionMode = ""
	fixture.payload.Release.Steps[0].ContainerImage = ""
	stageDir := filepath.Join(fixture.stateDir, "staging")
	t.Cleanup(func() { _ = os.Chmod(stageDir, 0700) })
	var input bytes.Buffer
	if err := tar.NewWriter(&input).Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(input.Bytes())
	fixture.server.Config.Handler = http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/agent/v1/deployments/42/staging/download":
				w.Header().
					Set("X-Artifact-SHA256", hex.EncodeToString(digest[:]))
				_, _ = w.Write(input.Bytes())
			case "/agent/v1/deployments/42/result":
				if !accepted {
					w.WriteHeader(http.StatusConflict)
					return
				}
				fixture.handle(w, r)
			default:
				fixture.handle(w, r)
			}
		},
	)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err = runPaired(ctx, client)
	var status *agentclient.StatusError
	if !errors.Is(err, executor.ErrContainerCleanup) ||
		(!accepted && (!errors.As(err, &status) || status.Status != 409)) {
		t.Fatalf("cleanup conflict lost its recovery barrier: %v", err)
	}
	if accepted {
		result := <-fixture.result
		if result.State != agentproto.ResultCleanupUnconfirmed ||
			result.Protocol != agentproto.AgentV3 {
			t.Fatalf(
				"host cleanup report used an invalid protocol: %s",
				result.Protocol,
			)
		}
	}
	select {
	case <-fixture.pollAgain:
		t.Fatal("polled with retained host staging")
	default:
	}
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); err != nil {
		t.Fatalf("cleanup failure lost its claim marker: %v", err)
	}
	client.Close()
	accepted = true
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	entries, err := os.ReadDir(stageDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("startup retained staging: %v, %v", entries, err)
	}
	resumeCtx, resumeCancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer resumeCancel()
	done := make(chan error, 1)
	go func() { done <- runPaired(resumeCtx, restarted) }()
	select {
	case <-fixture.pollAgain:
	case <-resumeCtx.Done():
		t.Fatal("polling did not resume after staging purge")
	}
	resumeCancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("stop recovered polling: %v", err)
	}
}
