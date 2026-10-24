package agentclient

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func testArtifactPin(data []byte) artifact.Pin {
	return artifact.Pin{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data))}
}

func TestPairedResourcePolicySnapshot(t *testing.T) {
	identity := testIdentity(t)
	legacy := testClient(t, "https://127.0.0.1:1", identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Artifact.DownloadBytes = 1234
	policy.Execution.MaxRetries = 12
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	policy.Artifact.DownloadBytes = 1
	policy.Execution.MaxRetries = 0
	policyCopy := client.ResourcePolicy()
	policyCopy.Artifact.DownloadBytes = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_ARTIFACT_DOWNLOAD_BYTES", "1")
	if client.ResourcePolicy().Artifact.DownloadBytes != 1234 || client.ExecutionPolicy().MaxRetries != 12 {
		t.Fatal("captured policy changed after construction")
	}
	if legacy.ResourcePolicy() != resourcepolicy.Defaults() {
		t.Fatal("legacy client lost default policy")
	}
}

func TestPairedResourcePolicyRejectsBeforeStateEffects(t *testing.T) {
	policy := resourcepolicy.Defaults()
	policy.Artifact.DownloadBytes = 0
	directory := filepath.Join(t.TempDir(), "not-created")
	if _, err := NewPairedWithResourcePolicy(directory, "test", executor.StartupSettings{}, policy); err == nil {
		t.Fatal("invalid resource policy accepted")
	}
	if _, err := os.Stat(directory); !os.IsNotExist(err) {
		t.Fatalf("invalid policy created state: %v", err)
	}
}

func TestArtifactDownloadIndependentClientBudgets(t *testing.T) {
	data := []byte("pinned package")
	fixture := artifactResponseFixture{data: data, pin: testArtifactPin(data)}
	identity := testIdentity(t)
	var requests atomic.Int32
	handler := artifactResponseHandler(t, fixture)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	results := make(chan artifactDownloadResult, 2)
	for _, budget := range []int64{int64(len(data)), int64(len(data) - 1)} {
		policy := resourcepolicy.Defaults()
		policy.Artifact.DownloadBytes = budget
		client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		go func() {
			file, err := client.Artifact(t.Context(), agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, fixture.pin)
			results <- artifactDownloadResult{file, err}
		}()
	}
	accepted, rejected := 0, 0
	for range 2 {
		result := <-results
		if errors.Is(result.err, artifact.ErrInvalid) && result.file == nil {
			rejected++
		} else {
			assertArtifactDownload(t, result, data)
			accepted++
		}
	}
	if accepted != 1 || rejected != 1 || requests.Load() != 1 {
		t.Fatalf("accepted=%d rejected=%d requests=%d", accepted, rejected, requests.Load())
	}
}

func TestArtifactDownloadRaisedBudgetReachesServer(t *testing.T) {
	identity := testIdentity(t)
	var requests atomic.Int32
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusConflict)
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Artifact.DownloadBytes = artifact.MaxDownload + 1
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	pin := testArtifactPin([]byte("pin"))
	pin.Size = policy.Artifact.DownloadBytes
	file, err := client.Artifact(t.Context(), agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, pin)
	var status *StatusError
	if file != nil || !errors.As(err, &status) || status.Status != http.StatusConflict || requests.Load() != 1 {
		t.Fatalf("raised pin did not reach server: file=%v error=%v requests=%d", file, err, requests.Load())
	}
	file, err = legacy.Artifact(t.Context(), agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, pin)
	if file != nil || !errors.Is(err, artifact.ErrInvalid) || requests.Load() != 1 {
		t.Fatalf("default pin reached server: file=%v error=%v requests=%d", file, err, requests.Load())
	}
}
