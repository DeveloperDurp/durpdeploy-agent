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

func TestArtifactDownloadUsesAuthenticatedPin(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong-bytes", "wrong-header", "wrong-size", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			data := []byte("pinned package")
			pin := artifact.Pin{
				SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data)),
			}
			identity := testIdentity(t)
			server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/agent/v1/deployments/42/artifact" {
					t.Errorf("unexpected endpoint: %s", r.URL.Path)
				}
				var request artifact.DownloadRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ClaimToken != "claim" {
					t.Errorf("claim authentication=%+v error=%v", request, err)
				}
				w.Header().Set("X-Artifact-SHA256", pin.SHA256)
				w.Header().Set("X-Artifact-Size", strconv.FormatInt(pin.Size, 10))
				switch scenario {
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
			})
			client := testClient(t, server.URL, identity.Fingerprint.String())
			file, err := client.Artifact(t.Context(), agentproto.PollResponse{
				DeploymentID: 42, ClaimToken: "claim",
			}, pin)
			if scenario == "valid" {
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := errors.Join(file.Close(), os.Remove(file.Name())); err != nil {
						t.Error(err)
					}
				})
				got, err := io.ReadAll(file)
				if err != nil || string(got) != string(data) {
					t.Fatalf("package bytes=%q error=%v", got, err)
				}
			} else {
				if err == nil || file != nil {
					t.Fatalf("accepted invalid response: file=%v error=%v", file, err)
				}
				entries, readErr := os.ReadDir(filepath.Join(client.StateDir(), "staging"))
				if readErr != nil || len(entries) != 0 {
					t.Fatalf("failed download retained files: %v %v", entries, readErr)
				}
			}
		})
	}
}
