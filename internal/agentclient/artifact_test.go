package agentclient

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type artifactResponseFixture struct {
	scenario string
	data     []byte
	pin      artifact.Pin
}

type artifactDownloadResult struct {
	file *os.File
	err  error
}

func TestArtifactDownloadUsesAuthenticatedPin(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-bytes", "wrong-header", "wrong-size", "redirect"} {
		t.Run(scenario, func(t *testing.T) { checkArtifactResponse(t, scenario) })
	}
}

func checkArtifactResponse(t *testing.T, scenario string) {
	t.Helper()
	data := []byte("pinned package")
	response := artifactResponseFixture{
		scenario: scenario, data: data,
		pin: artifact.Pin{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data))},
	}
	identity := testIdentity(t)
	server := newTLSServer(t, identity, artifactResponseHandler(t, response))
	client := testClient(t, server.URL, identity.Fingerprint.String())
	file, err := client.Artifact(t.Context(), agentproto.PollResponse{
		DeploymentID: 42, ClaimToken: "claim",
	}, response.pin)
	result := artifactDownloadResult{file, err}
	if scenario == "valid" {
		assertArtifactDownload(t, result, data)
		return
	}
	if result.err == nil || result.file != nil {
		t.Fatalf("accepted invalid response: file=%v error=%v", result.file, result.err)
	}
	entries, err := os.ReadDir(filepath.Join(client.StateDir(), "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed download retained files: %v %v", entries, err)
	}
}

func artifactResponseHandler(t *testing.T, fixture artifactResponseFixture) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/agent/v1/deployments/42/artifact" {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		var request artifact.DownloadRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ClaimToken != "claim" {
			t.Errorf("claim authentication=%+v error=%v", request, err)
		}
		w.Header().Set("X-Artifact-SHA256", fixture.pin.SHA256)
		w.Header().Set("X-Artifact-Size", strconv.FormatInt(fixture.pin.Size, 10))
		data := fixture.data
		switch fixture.scenario {
		case "wrong-bytes":
			data = []byte("changed bytes!")
		case "wrong-header":
			w.Header().Set("X-Artifact-SHA256", "incorrect")
		case "wrong-size":
			w.Header().Set("Content-Length", strconv.FormatInt(artifact.MaxDownload+1, 10))
			w.WriteHeader(200)
			return
		case "redirect":
			w.Header().Set("Location", "https://untrusted.example/package")
			w.WriteHeader(302)
			return
		}
		if _, err := w.Write(data); err != nil {
			t.Error(err)
		}
	}
}

func assertArtifactDownload(t *testing.T, result artifactDownloadResult, data []byte) {
	t.Helper()
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() {
		if err := errors.Join(result.file.Close(), os.Remove(result.file.Name())); err != nil {
			t.Error(err)
		}
	})
	got, err := io.ReadAll(result.file)
	if err != nil || string(got) != string(data) {
		t.Fatalf("package bytes=%q error=%v", got, err)
	}
}
