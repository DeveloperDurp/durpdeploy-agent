package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type claimPackageFixture struct {
	process   *agentSubprocessFixture
	client    *agentclient.Client
	scenario  string
	moved     string
	archive   []byte
	input     []byte
	pin       *artifact.Pin
	started   atomic.Bool
	downloads atomic.Int64
	mu        sync.Mutex
	output    []byte
}

func newClaimPackageFixture(t *testing.T, scenario string) *claimPackageFixture {
	t.Helper()
	process := newAgentSubprocessFixture(t,
		`test "$(cat "$ARTIFACT_PATH/input")" = release; test "$(stat -c %a "$ARTIFACT_PATH/input")" = 400; test "$(stat -c %a "$ARTIFACT_PATH")" = 500; cp "$ARTIFACT_PATH/input" "$DURPDEPLOY_STAGE_DIR/output"`)
	client, _ := claimLifecycleClient(t, process)
	result := &claimPackageFixture{process: process, client: client, scenario: scenario,
		moved: filepath.Join(t.TempDir(), "moved-package")}
	process.payload.Release.Staging = true
	process.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionHost
	process.payload.Release.Steps[0].ContainerImage = ""
	if scenario == "moved-package" {
		process.payload.Release.Steps[0].ScriptBody += fmt.Sprintf("; chmod 700 \"$ARTIFACT_PATH\"; mv \"$ARTIFACT_PATH\" '%s'", result.moved)
	}
	result.archive = claimPackageArchive(t, scenario)
	result.pin = &artifact.Pin{SHA256: fmt.Sprintf("%x", sha256.Sum256(result.archive)), Size: int64(len(result.archive))}
	process.payload.Release.Artifact = result.pin
	var input bytes.Buffer
	if err := tar.NewWriter(&input).Close(); err != nil {
		t.Fatal(err)
	}
	result.input = input.Bytes()
	process.startAcknowledged = func() { result.started.Store(true) }
	process.server.Config.Handler = http.HandlerFunc(result.handle)
	return result
}

func claimPackageArchive(t *testing.T, scenario string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
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
	return buffer.Bytes()
}

func (f *claimPackageFixture) handle(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/agent/v1/deployments/42/staging/download":
		w.Header().Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(f.input)))
		if _, err := w.Write(f.input); err != nil {
			f.process.t.Error(err)
		}
	case "/agent/v1/deployments/42/artifact":
		f.sendPackage(w)
	case "/agent/v1/deployments/42/staging/upload":
		f.receiveOutput(w, r)
	default:
		f.process.handle(w, r)
	}
}

func (f *claimPackageFixture) sendPackage(w http.ResponseWriter) {
	f.downloads.Add(1)
	if !f.started.Load() {
		f.process.t.Error("package requested before claim start")
	}
	w.Header().Set("X-Artifact-SHA256", f.pin.SHA256)
	w.Header().Set("X-Artifact-Size", strconv.FormatInt(f.pin.Size, 10))
	if f.scenario == "changed-pin" {
		w.Header().Set("X-Artifact-SHA256", "incorrect")
	}
	if _, err := w.Write(f.packageBytes()); err != nil {
		f.process.t.Error(err)
	}
}

func (f *claimPackageFixture) packageBytes() []byte {
	if f.scenario == "corrupt-bytes" {
		data := bytes.Clone(f.archive)
		// Change only the unused local-header timestamp. Contents and CRCs
		// remain valid, so ZIP validation cannot mask a missing hash check.
		data[10] ^= 1
		return data
	}
	return f.archive
}

func (f *claimPackageFixture) receiveOutput(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		f.process.t.Error(err)
	}
	f.mu.Lock()
	f.output = body
	f.mu.Unlock()
	w.WriteHeader(204)
}
