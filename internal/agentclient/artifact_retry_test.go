package agentclient

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestArtifactRetriesInterruptedBody(t *testing.T) {
	// Given an authenticated response interrupted after its first bytes.
	data := []byte("pinned package")
	pin := artifact.Pin{SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), Size: int64(len(data))}
	var calls atomic.Int32
	identity := testIdentity(t)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Artifact-SHA256", pin.SHA256)
		w.Header().Set("X-Artifact-Size", strconv.Itoa(len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		body := data
		if calls.Add(1) == 1 {
			body = data[:3]
		}
		if _, err := w.Write(body); err != nil {
			t.Error(err)
		}
	})
	client := testClient(t, server.URL, identity.Fingerprint.String())
	waits := 0
	client.sleep = func(context.Context, time.Duration) error {
		waits++
		entries, err := os.ReadDir(filepath.Join(client.StateDir(), "staging"))
		if err != nil || len(entries) != 0 {
			t.Fatalf("partial download retained before retry: %v %v", entries, err)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// When the job downloads its pinned input.
	file, err := client.Artifact(ctx, agentproto.PollResponse{
		DeploymentID: 42, ClaimToken: "claim",
	}, pin)

	// Then the retry returns complete bytes and removes the partial file.
	assertArtifactDownload(t, artifactDownloadResult{file, err}, data)
	if calls.Load() != 2 || waits != 1 {
		t.Fatalf("requests=%d retry waits=%d", calls.Load(), waits)
	}
}
