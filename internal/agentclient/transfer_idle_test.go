package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestConfiguredTransferIdleTimeout(t *testing.T) {
	for _, mode := range []string{"artifact", "download", "upload"} {
		t.Run(mode, func(t *testing.T) {
			archive := stagingArchive(t)
			pin := testArtifactPin(archive)
			identity := testIdentity(t)
			server := newTLSServer(t, identity, delayedTransferHandler(t, mode, archive, pin.SHA256))
			legacy := testClient(t, server.URL, identity.Fingerprint.String())
			for _, seconds := range []int64{1, 5} {
				t.Run(strconv.FormatInt(seconds, 10), func(t *testing.T) {
					checkConfiguredIdleTransfer(t, legacy, mode, seconds, archive)
				})
			}
		})
	}
}

func delayedTransferHandler(t *testing.T, mode string, archive []byte, checksum string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if mode != "artifact" && r.Header.Get("X-DurpDeploy-Claim") != "claim" {
			t.Error("missing staging claim")
		}
		if mode == "upload" {
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(data, archive) {
				t.Errorf("changed upload: %v", err)
			}
		} else {
			w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
			w.Header().Set("X-Artifact-Size", strconv.Itoa(len(archive)))
			w.Header().Set("X-Artifact-SHA256", checksum)
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
		}
		select {
		case <-time.After(1500 * time.Millisecond):
			if mode == "upload" {
				w.WriteHeader(http.StatusNoContent)
			} else if _, err := w.Write(archive); err != nil {
				t.Error(err)
			}
		case <-r.Context().Done():
		}
	}
}

func checkConfiguredIdleTransfer(t *testing.T, legacy *Client, mode string, seconds int64, archive []byte) {
	t.Helper()
	policy := resourcepolicy.Defaults()
	policy.Transfer.IdleTimeoutSeconds = seconds
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "test", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	policy.Transfer.IdleTimeoutSeconds = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_TRANSFER_IDLE_TIMEOUT_SECONDS", "1")
	var waits int
	client.sleep = func(context.Context, time.Duration) error { waits++; return context.Canceled }
	var upload *os.File
	if mode == "upload" {
		upload, err = os.CreateTemp(t.TempDir(), "upload-*.tar")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = upload.Close() }()
		if _, err := upload.Write(archive); err != nil {
			t.Fatal(err)
		}
	}
	claim := agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}
	var file *os.File
	if mode == "artifact" {
		file, err = client.Artifact(t.Context(), claim, testArtifactPin(archive))
	} else {
		file, err = client.Staging(t.Context(), claim, upload)
	}
	if seconds == 1 {
		if file != nil || !errors.Is(err, context.Canceled) || waits != 1 {
			t.Fatalf("stalled transfer did not reject: file=%v error=%v waits=%d", file, err, waits)
		}
	} else if err != nil {
		t.Fatal(err)
	} else if mode != "upload" {
		assertIdleTransferBytes(t, mode, file, archive)
	}
	assertTransferStagingEmpty(t, client)
}

func assertIdleTransferBytes(t *testing.T, mode string, file *os.File, archive []byte) {
	t.Helper()
	data, readErr := io.ReadAll(file)
	var removeErr error
	if mode == "artifact" {
		removeErr = os.Remove(file.Name())
	}
	closeErr := file.Close()
	if !bytes.Equal(data, archive) || errors.Join(readErr, removeErr, closeErr) != nil {
		t.Fatalf("changed transfer bytes or cleanup: %v", errors.Join(readErr, removeErr, closeErr))
	}
}

func TestStagingProgressOutlivesArtifactFetchBudget(t *testing.T) {
	archive := stagingArchive(t)
	identity := testIdentity(t)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
		w.Header().Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive)))
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for start := 0; start < len(archive); start += len(archive) / 4 {
			select {
			case <-time.After(400 * time.Millisecond):
			case <-r.Context().Done():
				return
			}
			if _, err := w.Write(archive[start : start+len(archive)/4]); err != nil {
				t.Error(err)
				return
			}
			w.(http.Flusher).Flush()
		}
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Artifact.FetchTimeoutSeconds = 1
	policy.Transfer.IdleTimeoutSeconds = 1
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "test", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	started := time.Now()
	file, err := client.Staging(ctx, agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if time.Since(started) < time.Second {
		t.Fatal("fixture did not outlive the artifact fetch budget")
	}
	assertTransferStagingEmpty(t, client)
}

func TestArtifactFetchBudgetIncludesRetryBackoff(t *testing.T) {
	identity := testIdentity(t)
	var requests atomic.Int32
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "10")
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Artifact.FetchTimeoutSeconds = 1
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "test", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	file, err := client.Artifact(t.Context(), agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, testArtifactPin([]byte("package")))
	if file != nil || !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 1 {
		t.Fatalf("retry escaped fetch budget: file=%v error=%v requests=%d", file, err, requests.Load())
	}
	assertTransferStagingEmpty(t, client)
}
