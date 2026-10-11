//go:build linux

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestHostSupervisorFailureStopsAgentUntilRestart(t *testing.T) {
	fixture := newAgentSubprocessFixture(
		t,
		`printf retained > "$DURPDEPLOY_STAGE_DIR/data"; kill -KILL "$PPID"; exit 0`,
	)
	client, _ := claimLifecycleClient(t, fixture)
	client.Close()
	fixture.payload.Release.Staging = true
	fixture.payload.Release.Steps[0].ExecutionMode = ""
	fixture.payload.Release.Steps[0].ContainerImage = ""
	var archive bytes.Buffer
	if err := tar.NewWriter(&archive).Close(); err != nil {
		t.Fatal(err)
	}
	fixture.server.Config.Handler = http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/agent/v1/deployments/42/staging/download" {
				w.Header().Set("X-Artifact-SHA256",
					fmt.Sprintf("%x", sha256.Sum256(archive.Bytes())))
				_, _ = w.Write(archive.Bytes())
				return
			}
			fixture.handle(w, r)
		})
	configuration := config{stateDir: fixture.stateDir, agentVersion: "test"}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := run(
		ctx,
		configuration,
	); !errors.Is(
		err,
		executor.ErrHostProcessCleanup,
	) {
		t.Fatalf("unconfirmed supervisor did not stop the agent: %v", err)
	}
	select {
	case <-fixture.result:
		t.Fatal("reported completion with unconfirmed process cleanup")
	case <-fixture.pollAgain:
		t.Fatal("polled again with unconfirmed process cleanup")
	default:
	}
	files, err := filepath.Glob(filepath.Join(fixture.stateDir,
		"staging", "claim-*", "workspace", "data"))
	if err != nil || len(files) != 1 {
		t.Fatalf("unconfirmed cleanup purged staging: %v %v", files, err)
	}
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); err != nil {
		t.Fatalf("lost the saved recovery claim: %v", err)
	}
	restartCtx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- run(restartCtx, configuration) }()
	select {
	case result := <-fixture.result:
		if result.State != agentproto.ResultFailed {
			t.Errorf("restart replayed result %s", result.State)
		}
	case <-time.After(5 * time.Second):
		stop()
		<-done
		t.Fatal("restart did not recover the saved result")
	}
	stop()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(fixture.stateDir, "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("restart retained staging: %v %v", entries, err)
	}
}
