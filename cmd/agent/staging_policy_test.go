package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestClaimStagingCapturedBudgetsAndCleanup(t *testing.T) {
	for _, scenario := range []string{"exact", "input-payload", "input-entries", "input-archive", "output-payload", "output-entries", "output-archive"} {
		t.Run(scenario, func(t *testing.T) { checkClaimStagingPolicy(t, scenario) })
	}
}

func checkClaimStagingPolicy(t *testing.T, scenario string) {
	t.Helper()
	script := `test "$(cat "$DURPDEPLOY_STAGE_DIR/nested/input")" = shared; rm -rf "$DURPDEPLOY_STAGE_DIR/nested"; printf result > "$DURPDEPLOY_STAGE_DIR/output"; echo policy-script-ran`
	switch scenario {
	case "output-payload":
		script += `; printf excessive > "$DURPDEPLOY_STAGE_DIR/output"`
	case "output-entries":
		script += `; touch "$DURPDEPLOY_STAGE_DIR/extra1" "$DURPDEPLOY_STAGE_DIR/extra2"`
	case "output-archive":
		script += `; rm "$DURPDEPLOY_STAGE_DIR/output"; touch "$DURPDEPLOY_STAGE_DIR/a" "$DURPDEPLOY_STAGE_DIR/b" "$DURPDEPLOY_STAGE_DIR/c" "$DURPDEPLOY_STAGE_DIR/d"`
	}
	fixture := newAgentSubprocessFixture(t, script)
	legacy, runtimeDirectory := claimLifecycleClient(t, fixture)
	legacy.Close()
	fixture.payload.Release.Staging = true
	fixture.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionHost
	fixture.payload.Release.Steps[0].ContainerImage = ""
	input := t.TempDir()
	if err := os.Mkdir(filepath.Join(input, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(input, "nested", "input"), []byte("shared"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := staging.Pack(t.Context(), input, &archive); err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Defaults()
	policy.Staging = resourcepolicy.Staging{PayloadBytes: 6, Entries: 2, ArchiveBytes: int64(archive.Len())}
	switch scenario {
	case "input-payload":
		policy.Staging.PayloadBytes--
	case "input-entries":
		policy.Staging.Entries--
	case "input-archive":
		policy.Staging.ArchiveBytes--
	case "output-entries":
		policy.Staging.ArchiveBytes = 4096
	case "output-archive":
		policy.Staging.Entries = 4
	}
	client, err := agentclient.NewPairedWithResourcePolicy(fixture.stateDir, "configured", executor.StartupSettings{ExecutionBoundary: "service"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if err := client.EnableContainers(t.Context(), executor.ContainerConfig{
		Runtime: agentproto.RuntimeDocker, SocketURL: "unix://" + filepath.Join(runtimeDirectory, "runtime.sock"),
	}); err != nil {
		t.Fatal(err)
	}
	policy.Staging.PayloadBytes = 1
	policyCopy := client.ResourcePolicy()
	policyCopy.Staging.Entries = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_STAGING_PAYLOAD_BYTES", "1")
	var uploads atomic.Int32
	fixture.server.Config.Handler = withClaimPolicy(t, client.ResourcePolicy(), claimStagingPolicyHandler(t, fixture, &archive, &uploads))
	claim, err := client.Poll(t.Context())
	if err != nil || claim == nil {
		t.Fatalf("poll: %v %v", claim, err)
	}
	if err := executeClaim(t.Context(), client, *claim); err != nil {
		t.Fatal(err)
	}
	result := <-fixture.result
	want := agentproto.ResultFailed
	var wantUploads int32
	if scenario == "exact" {
		want = agentproto.ResultSucceeded
		wantUploads = 1
	}
	if result.State != want || uploads.Load() != wantUploads {
		t.Fatalf("scenario=%s result=%s uploads=%d", scenario, result.State, uploads.Load())
	}
	if strings.HasPrefix(scenario, "input-") && len(fixture.logs()) != 0 {
		t.Fatal("rejected input reached script execution")
	}
	if strings.HasPrefix(scenario, "output-") && len(fixture.logs()) == 0 {
		t.Fatal("output rejection did not exercise script execution")
	}
	assertPackageDirectoryEmpty(t, filepath.Join(fixture.stateDir, "staging"))
}

func claimStagingPolicyHandler(t *testing.T, fixture *agentSubprocessFixture, archive *bytes.Buffer, uploads *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent/v1/deployments/42/staging/download":
			w.Header().Set("Content-Length", fmt.Sprint(archive.Len()))
			w.Header().Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive.Bytes())))
			if _, err := w.Write(archive.Bytes()); err != nil {
				t.Error(err)
			}
		case "/agent/v1/deployments/42/staging/upload":
			uploads.Add(1)
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			assertClaimPolicyOutput(t, body)
			w.WriteHeader(http.StatusNoContent)
		case "/agent/v1/deployments/42/result":
			entries, err := os.ReadDir(filepath.Join(fixture.stateDir, "staging"))
			if err != nil || len(entries) != 0 {
				t.Errorf("terminal report preceded cleanup: %v %v", entries, err)
			}
			fixture.handle(w, r)
		default:
			fixture.handle(w, r)
		}
	})
}

func assertClaimPolicyOutput(t *testing.T, body []byte) {
	t.Helper()
	output := t.TempDir()
	if err := staging.Unpack(t.Context(), bytes.NewReader(body), output); err != nil {
		t.Error(err)
		return
	}
	data, err := os.ReadFile(filepath.Join(output, "output"))
	if err != nil || string(data) != "result" {
		t.Errorf("published output=%q error=%v", data, err)
	}
}
