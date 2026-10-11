package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClaimStagingPublishesBeforeResultAndPurges(t *testing.T) {
	for _, scenario := range []string{"success", "upload-conflict", "invalid-output", "renamed-directory", "escaped-directory", "renamed-root", "escaped-root-workspace"} {
		t.Run(scenario, func(t *testing.T) {
			script := `test "$(cat "$DURPDEPLOY_STAGE_DIR/input")" = shared; rm "$DURPDEPLOY_STAGE_DIR/input"; printf result > "$DURPDEPLOY_STAGE_DIR/output"`
			if scenario == "invalid-output" {
				script += `; ln -s /etc/passwd "$DURPDEPLOY_STAGE_DIR/link"`
			}
			if scenario == "renamed-directory" {
				script += `; mv "$DURPDEPLOY_STAGE_DIR" "$DURPDEPLOY_STAGE_DIR-renamed"; mkdir "$DURPDEPLOY_STAGE_DIR"; printf incorrect > "$DURPDEPLOY_STAGE_DIR/output"`
			}
			if scenario == "escaped-directory" {
				script += `; mv "$DURPDEPLOY_STAGE_DIR" "$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")")/retained"`
			}
			if scenario == "renamed-root" {
				script += `; root=$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")"); mv "$root" "$(dirname "$root")/retained"`
			}
			if scenario == "escaped-root-workspace" {
				script += `; root=$(dirname "$(dirname "$DURPDEPLOY_STAGE_DIR")"); mv "$DURPDEPLOY_STAGE_DIR" "$(dirname "$root")/retained"`
			}
			fixture := newAgentSubprocessFixture(t, script)
			client, _ := claimLifecycleClient(t, fixture)
			fixture.payload.Release.Staging = true
			fixture.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionHost
			fixture.payload.Release.Steps[0].ContainerImage = ""
			input := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(input, "input"),
				[]byte("shared"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := staging.Pack(t.Context(), input, &archive); err != nil {
				t.Fatal(err)
			}
			var mu sync.Mutex
			var uploaded []byte
			fixture.server.Config.Handler = http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					switch r.URL.Path {
					case "/agent/v1/deployments/42/staging/download":
						w.Header().
							Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive.Bytes())))
						if _, err := w.Write(archive.Bytes()); err != nil {
							t.Error(err)
						}
					case "/agent/v1/deployments/42/staging/upload":
						body, err := io.ReadAll(r.Body)
						if err != nil {
							t.Error(err)
						}
						mu.Lock()
						uploaded = body
						mu.Unlock()
						if scenario == "upload-conflict" {
							w.WriteHeader(http.StatusConflict)
							return
						}
						w.WriteHeader(http.StatusNoContent)
					case "/agent/v1/deployments/42/result":
						mu.Lock()
						if scenario == "success" && len(uploaded) == 0 {
							t.Error(
								"reported success before publishing staging",
							)
						}
						mu.Unlock()
						entries, err := os.ReadDir(
							filepath.Join(fixture.stateDir, "staging"),
						)
						if err != nil || len(entries) != 0 {
							t.Errorf(
								"terminal report retained staging: %v %v",
								entries,
								err,
							)
						}
						if scenario == "renamed-root" ||
							scenario == "escaped-root-workspace" {
							entries, err := os.ReadDir(filepath.Join(
								fixture.stateDir, "retained"))
							if err != nil || len(entries) != 0 {
								t.Errorf(
									"renamed root retained files: %v %v",
									entries,
									err,
								)
							}
						}
						fixture.handle(w, r)
					default:
						fixture.handle(w, r)
					}
				},
			)
			claim, err := client.Poll(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if err := executeClaim(t.Context(), client, *claim); err != nil {
				t.Fatal(err)
			}
			result := <-fixture.result
			want := agentproto.ResultSucceeded
			if scenario != "success" {
				want = agentproto.ResultFailed
			}
			if result.State != want {
				t.Fatalf("result=%s want=%s", result.State, want)
			}
			mu.Lock()
			body := append([]byte(nil), uploaded...)
			mu.Unlock()
			if scenario == "invalid-output" ||
				scenario == "renamed-directory" ||
				scenario == "escaped-directory" || scenario == "renamed-root" || scenario == "escaped-root-workspace" {
				if len(body) != 0 {
					t.Fatal("published invalid staging output")
				}
				return
			}
			output := t.TempDir()
			if err := staging.Unpack(
				t.Context(),
				bytes.NewReader(body),
				output,
			); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(output, "output"))
			if err != nil || string(data) != "result" {
				t.Fatalf("published result=%q err=%v", data, err)
			}
		})
	}
}
