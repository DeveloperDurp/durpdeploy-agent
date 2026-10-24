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
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestStagingTransferCapturedBudgets(t *testing.T) {
	for _, scenario := range []string{"exact", "payload", "entries", "archive"} {
		t.Run(scenario, func(t *testing.T) { checkStagingTransferBudget(t, scenario) })
	}
}

func checkStagingTransferBudget(t *testing.T, scenario string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "data"), []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := staging.Pack(t.Context(), directory, &archive); err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t)
	var requests atomic.Int32
	server := newTLSServer(t, identity, stagingPolicyHandler(t, &archive, &requests))
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Staging = resourcepolicy.Staging{PayloadBytes: 8, Entries: 2, ArchiveBytes: int64(archive.Len())}
	switch scenario {
	case "payload":
		policy.Staging.PayloadBytes--
	case "entries":
		policy.Staging.Entries--
	case "archive":
		policy.Staging.ArchiveBytes--
	}
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	policy.Staging.PayloadBytes = 1
	policyCopy := client.ResourcePolicy()
	policyCopy.Staging.Entries = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_STAGING_PAYLOAD_BYTES", "1")
	claim := agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}
	file, err := client.Staging(t.Context(), claim, nil)
	if scenario != "exact" {
		if file != nil || !errors.Is(err, staging.ErrInvalid) || requests.Load() != 1 {
			t.Fatalf("excessive %s: file=%v error=%v requests=%d", scenario, file, err, requests.Load())
		}
	} else {
		assertStagingPolicyTransfer(t, client, claim, file, err)
		if requests.Load() != 2 {
			t.Fatalf("exact download and upload requests=%d", requests.Load())
		}
	}
	entries, err := os.ReadDir(filepath.Join(client.StateDir(), "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("transfer retained named spools: %v %v", entries, err)
	}
}

func stagingPolicyHandler(t *testing.T, archive *bytes.Buffer, requests *atomic.Int32) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-DurpDeploy-Claim") != "claim" {
			t.Error("missing claim authentication")
		}
		if r.URL.Path == "/agent/v1/deployments/42/staging/upload" {
			data, err := io.ReadAll(r.Body)
			if err != nil || !bytes.Equal(data, archive.Bytes()) {
				t.Errorf("uploaded archive differs: %v", err)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(archive.Len()))
		w.Header().Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive.Bytes())))
		if _, err := w.Write(archive.Bytes()); err != nil {
			t.Error(err)
		}
	}
}

func assertStagingPolicyTransfer(t *testing.T, client *Client, claim agentproto.PollResponse, file *os.File, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("download retained a named spool: %v", err)
	}
	if _, err := client.Staging(t.Context(), claim, file); err != nil {
		t.Fatal(err)
	}
}

type stagingAdmissionTransport struct{ requests int }

func (transport *stagingAdmissionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests++
	if request.Body != nil {
		_ = request.Body.Close()
	}
	return nil, io.ErrUnexpectedEOF
}

func TestStagingUploadCapturedAndRaisedArchiveBudget(t *testing.T) {
	identity := testIdentity(t)
	legacy := testClient(t, "https://127.0.0.1", identity.Fingerprint.String())
	for _, size := range []int64{2048, staging.MaxArchive + 1} {
		policy := resourcepolicy.Defaults()
		policy.Staging.ArchiveBytes = size
		if size < policy.Staging.PayloadBytes+1024 {
			policy.Staging.PayloadBytes = 1
		}
		client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		probe := new(stagingAdmissionTransport)
		client.http.Transport = probe
		client.sleep = func(context.Context, time.Duration) error { return context.Canceled }
		file, err := os.CreateTemp(t.TempDir(), "sparse-upload")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		for _, extra := range []int64{0, 1} {
			if err := file.Truncate(size + extra); err != nil {
				t.Fatal(err)
			}
			_, err := client.Staging(t.Context(), agentproto.PollResponse{DeploymentID: 42}, file)
			if extra == 0 && (!errors.Is(err, context.Canceled) || probe.requests != 1) {
				t.Fatalf("exact upload did not reach transport: %v requests=%d", err, probe.requests)
			}
			if extra == 1 && (!errors.Is(err, staging.ErrInvalid) || probe.requests != 1) {
				t.Fatalf("excessive upload reached transport: %v requests=%d", err, probe.requests)
			}
		}
	}
}

type stagingReadProbe struct {
	io.ReadCloser
	bytes *atomic.Int64
}

func (reader stagingReadProbe) Read(data []byte) (int, error) {
	n, err := reader.ReadCloser.Read(data)
	reader.bytes.Add(int64(n))
	return n, err
}

type stagingReadTransport struct {
	http.RoundTripper
	bytes atomic.Int64
}

func (transport *stagingReadTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.RoundTripper.RoundTrip(request)
	if err == nil {
		response.Body = stagingReadProbe{ReadCloser: response.Body, bytes: &transport.bytes}
	}
	return response, err
}

func TestStagingDownloadBoundsStreamAndAllowsRaisedHeader(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprint(raised), func(t *testing.T) {
			checkStagingDownloadBound(t, raised)
		})
	}
}

func checkStagingDownloadBound(t *testing.T, raised bool) {
	t.Helper()
	identity := testIdentity(t)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, _ *http.Request) {
		if raised {
			w.Header().Set("Content-Length", fmt.Sprint(staging.MaxArchive+1))
		} else {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			_, _ = w.Write(make([]byte, 64<<10))
		}
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	policy := resourcepolicy.Defaults()
	policy.Staging = resourcepolicy.Staging{PayloadBytes: 1, Entries: 1, ArchiveBytes: 2048}
	if raised {
		policy.Staging.ArchiveBytes = staging.MaxArchive + 1
	}
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	probe := &stagingReadTransport{RoundTripper: client.http.Transport}
	client.http.Transport = probe
	client.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	_, err = client.Staging(t.Context(), agentproto.PollResponse{DeploymentID: 42}, nil)
	if raised {
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("raised header did not reach body handling: %v", err)
		}
	} else if !errors.Is(err, staging.ErrInvalid) || probe.bytes.Load() != policy.Staging.ArchiveBytes+1 {
		t.Fatalf("unbounded stream read=%d error=%v", probe.bytes.Load(), err)
	}
}
