package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClaimPackageBecomesWritableStagingOutput(t *testing.T) {
	for _, scenario := range []string{"success", "moved-package", "invalid-zip", "changed-pin"} {
		t.Run(scenario, func(t *testing.T) {
			fixture := newAgentSubprocessFixture(t,
				`test "$(cat "$ARTIFACT_PATH/input")" = release; test "$(stat -c %a "$ARTIFACT_PATH/input")" = 444; test "$(stat -c %a "$ARTIFACT_PATH")" = 555; cp "$ARTIFACT_PATH/input" "$DURPDEPLOY_STAGE_DIR/output"`)
			client, _ := claimLifecycleClient(t, fixture)
			fixture.payload.Release.Staging = true
			fixture.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionHost
			fixture.payload.Release.Steps[0].ContainerImage = ""
			moved := filepath.Join(t.TempDir(), "moved-package")
			if scenario == "moved-package" {
				fixture.payload.Release.Steps[0].ScriptBody += fmt.Sprintf("; chmod 700 \"$ARTIFACT_PATH\"; mv \"$ARTIFACT_PATH\" '%s'", moved)
			}
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			name := "input"
			if scenario == "invalid-zip" {
				name = "../escape"
			}
			entry, err := writer.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := entry.Write([]byte("release")); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			pin := &artifact.Pin{
				SHA256: fmt.Sprintf("%x", sha256.Sum256(archive.Bytes())),
				Size:   int64(archive.Len()),
			}
			fixture.payload.Release.Artifact = pin
			var input bytes.Buffer
			if err := tar.NewWriter(&input).Close(); err != nil {
				t.Fatal(err)
			}
			var started atomic.Bool
			fixture.startAcknowledged = func() { started.Store(true) }
			var mu sync.Mutex
			var output []byte
			fixture.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/agent/v1/deployments/42/staging/download":
					w.Header().Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(input.Bytes())))
					if _, err := w.Write(input.Bytes()); err != nil {
						t.Error(err)
					}
				case "/agent/v1/deployments/42/artifact":
					if !started.Load() {
						t.Error("package requested before claim start")
					}
					w.Header().Set("X-Artifact-SHA256", pin.SHA256)
					w.Header().Set("X-Artifact-Size", strconv.FormatInt(pin.Size, 10))
					if scenario == "changed-pin" {
						w.Header().Set("X-Artifact-SHA256", "incorrect")
					}
					if _, err := w.Write(archive.Bytes()); err != nil {
						t.Error(err)
					}
				case "/agent/v1/deployments/42/staging/upload":
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					mu.Lock()
					output = body
					mu.Unlock()
					w.WriteHeader(204)
				default:
					fixture.handle(w, r)
				}
			})
			claim, err := client.Poll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := executeClaim(t.Context(), client, *claim); err != nil {
				t.Fatal(err)
			}
			result := <-fixture.result
			want := agentproto.ResultSucceeded
			if scenario == "invalid-zip" || scenario == "changed-pin" {
				want = agentproto.ResultFailed
			}
			if result.State != want {
				t.Fatalf("result=%+v want=%s", result, want)
			}
			mu.Lock()
			body := append([]byte(nil), output...)
			mu.Unlock()
			if want == agentproto.ResultSucceeded {
				directory := t.TempDir()
				if err := staging.Unpack(t.Context(), bytes.NewReader(body), directory); err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(directory, "output"))
				if err != nil || string(data) != "release" {
					t.Fatalf("published output=%q error=%v", data, err)
				}
				if _, err := os.Stat(filepath.Join(directory, "input")); !os.IsNotExist(err) {
					t.Fatalf("original package leaked into staging: %v", err)
				}
			} else if len(body) != 0 {
				t.Fatal("failed package was published")
			}
			entries, err := os.ReadDir(filepath.Join(fixture.stateDir, "staging"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("terminal result retained managed files: %v %v", entries, err)
			}
			if scenario == "moved-package" {
				entries, err := os.ReadDir(moved)
				if err != nil || len(entries) != 0 {
					t.Fatalf("moved managed package retained files: %v %v", entries, err)
				}
			}
		})
	}
}
