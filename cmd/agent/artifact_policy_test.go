package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestClaimArtifactFetchTimeoutCleansWithoutExecution(t *testing.T) {
	fixture := newClaimPackageFixture(t, "success")
	fixture.process.payload.Release.Steps[0].ExecutionMode = ""
	fixture.client.Close()
	policy := resourcepolicy.Defaults()
	policy.Artifact.FetchTimeoutSeconds = 1
	client, err := agentclient.NewPairedWithResourcePolicy(fixture.process.stateDir, "configured", executor.StartupSettings{ExecutionBoundary: "service"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	fixture.process.server.Config.Handler = withClaimPolicy(t, policy, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/deployments/42/artifact" {
			fixture.handle(w, r)
			return
		}
		fixture.downloads.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(fixture.pin.Size))
		w.Header().Set("X-Artifact-Size", fmt.Sprint(fixture.pin.Size))
		w.Header().Set("X-Artifact-SHA256", fixture.pin.SHA256)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	claim, err := client.Poll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := executeClaim(t.Context(), client, *claim); err != nil {
		t.Fatal(err)
	}
	result := <-fixture.process.result
	if result.State != agentproto.ResultFailed || fixture.downloads.Load() != 1 {
		t.Fatalf("timeout result=%+v downloads=%d", result, fixture.downloads.Load())
	}
	fixture.mu.Lock()
	outputBytes := len(fixture.output)
	fixture.mu.Unlock()
	if outputBytes != 0 || len(fixture.process.logs()) != 0 {
		t.Fatal("timed-out package executed or published output")
	}
	assertPackageDirectoryEmpty(t, filepath.Join(fixture.process.stateDir, "staging"))
}

func TestClaimArtifactPolicyBudgetsAndCleanup(t *testing.T) {
	for _, scenario := range []string{"exact", "download", "extraction", "entries", "nodes", "metadata"} {
		t.Run(scenario, func(t *testing.T) { checkClaimArtifactPolicy(t, scenario) })
	}
}

func checkClaimArtifactPolicy(t *testing.T, scenario string) {
	t.Helper()
	fixture := newClaimPackageFixture(t, "success")
	fixture.client.Close()
	fixture.archive = artifactPolicyArchive(t)
	fixture.pin = &artifact.Pin{SHA256: fmt.Sprintf("%x", sha256.Sum256(fixture.archive)), Size: int64(len(fixture.archive))}
	fixture.process.payload.Release.Artifact = fixture.pin
	fixture.process.payload.Release.Steps[0].ExecutionMode = ""
	policy := resourcepolicy.Defaults()
	policy.Artifact.DownloadBytes = fixture.pin.Size
	policy.Artifact.ExtractedBytes = 8
	policy.Artifact.Files = 2
	policy.Artifact.StagingNodes = 3
	switch scenario {
	case "download":
		policy.Artifact.DownloadBytes--
	case "extraction":
		policy.Artifact.ExtractedBytes--
	case "entries":
		policy.Artifact.Files--
	case "nodes":
		policy.Artifact.StagingNodes--
	case "metadata":
		policy.Artifact.MetadataBytes = 1
	}
	client, err := agentclient.NewPairedWithResourcePolicy(fixture.process.stateDir, "configured", executor.StartupSettings{ExecutionBoundary: "service"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	fixture.client = client
	fixture.process.server.Config.Handler = withClaimPolicy(t, policy, http.HandlerFunc(fixture.handle))
	policy.Artifact.ExtractedBytes = 1
	policyCopy := client.ResourcePolicy()
	policyCopy.Artifact.ExtractedBytes = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_ARTIFACT_EXTRACTED_BYTES", "1")
	claim, err := client.Poll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	err = executeClaim(t.Context(), client, *claim)
	if scenario == "download" {
		if !errors.Is(err, artifact.ErrInvalid) || fixture.started.Load() || fixture.downloads.Load() != 0 {
			t.Fatalf("excessive pin reached claim start: error=%v started=%v downloads=%d", err, fixture.started.Load(), fixture.downloads.Load())
		}
		assertPackageDirectoryEmpty(t, filepath.Join(fixture.process.stateDir, "staging"))
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	result := <-fixture.process.result
	want := agentproto.ResultFailed
	if scenario == "exact" {
		want = agentproto.ResultSucceeded
	}
	if result.State != want || fixture.downloads.Load() != 1 {
		t.Fatalf("result=%+v downloads=%d", result, fixture.downloads.Load())
	}
	fixture.mu.Lock()
	output := append([]byte(nil), fixture.output...)
	fixture.mu.Unlock()
	if scenario == "exact" {
		assertPackagePublication(t, output)
	} else if len(output) != 0 || len(fixture.process.logs()) != 0 {
		t.Fatal("rejected package executed or published output")
	}
	assertPackageDirectoryEmpty(t, filepath.Join(fixture.process.stateDir, "staging"))
}

func artifactPolicyArchive(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, file := range []struct{ name, data string }{{"input", "release"}, {"nested/second", "x"}} {
		entry, err := writer.Create(file.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(file.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestPayloadArtifactRaisedDownloadPolicy(t *testing.T) {
	fixture := newClaimPackageFixture(t, "success")
	fixture.process.payload.Release.Artifact.Size = artifact.MaxDownload + 1
	raw, err := json.Marshal(fixture.process.payload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodePayload(raw, 42); !errors.Is(err, artifact.ErrInvalid) {
		t.Fatalf("default policy accepted excessive pin: %v", err)
	}
	policy := resourcepolicy.Defaults()
	policy.Artifact.DownloadBytes++
	if _, err := decodePayloadWithResourcePolicy(raw, 42, policy); err != nil {
		t.Fatalf("raised download budget rejected: %v", err)
	}
	policy.Artifact.DownloadBytes = 0
	if _, err := decodePayloadWithResourcePolicy(raw, 42, policy); err == nil {
		t.Fatal("invalid resource policy accepted")
	}
}
