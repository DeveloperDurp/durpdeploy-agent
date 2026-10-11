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
	"strconv"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func stagingArchive(t *testing.T) []byte {
	t.Helper()
	directory := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(directory, "result"),
		[]byte("shared"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := staging.Pack(t.Context(), directory, &archive); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func TestStagingTransfersRetryAndKeepArchivesPrivate(t *testing.T) {
	archive := stagingArchive(t)
	identity := testIdentity(t)
	now := time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)
	downloads, uploads := 0, 0
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost ||
				r.Header.Get("X-DurpDeploy-Claim") != "claim-secret" {
				t.Error("missing claim authentication")
			}
			switch r.URL.Path {
			case "/agent/v1/deployments/42/staging/download":
				downloads++
				if downloads == 1 {
					w.Header().Set("Retry-After", "5")
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
				w.Header().
					Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive)))
				if _, err := w.Write(archive); err != nil {
					t.Error(err)
				}
			case "/agent/v1/deployments/42/staging/upload":
				uploads++
				body, err := io.ReadAll(r.Body)
				if err != nil || !bytes.Equal(body, archive) ||
					r.ContentLength != int64(len(archive)) {
					t.Errorf("upload differs from downloaded archive: %v", err)
				}
				if uploads == 1 {
					w.Header().
						Set("Retry-After", now.Add(time.Minute).Format(http.TimeFormat))
					w.WriteHeader(http.StatusTooManyRequests)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			default:
				t.Error("unexpected transfer path", r.URL.Path)
				w.WriteHeader(http.StatusNotFound)
			}
		},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	client.now = func() time.Time { return now }
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	claim := agentproto.PollResponse{
		DeploymentID: 42,
		ClaimToken:   "claim-secret",
	}
	file, err := client.Staging(t.Context(), claim, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := os.Stat(file.Name()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("downloaded archive remains on disk: %v", err)
	}
	if _, err := client.Staging(t.Context(), claim, file); err != nil {
		t.Fatal(err)
	}
	if downloads != 2 || uploads != 2 {
		t.Fatalf("downloads=%d uploads=%d", downloads, uploads)
	}
	if len(delays) != 2 || delays[0] != 5*time.Second ||
		delays[1] != time.Minute {
		t.Fatalf("ignored Retry-After: %v", delays)
	}
}

func TestStagingRejectsInvalidDownloads(t *testing.T) {
	archive := stagingArchive(t)
	for _, scenario := range []string{"checksum", "archive", "truncated", "forbidden", "missing-directory"} {
		t.Run(scenario, func(t *testing.T) {
			identity := testIdentity(t)
			server := newTLSServer(
				t,
				identity,
				func(w http.ResponseWriter, r *http.Request) {
					if scenario == "forbidden" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					body := archive
					if scenario == "archive" {
						body = []byte("invalid TAR input")
					}
					length := len(body)
					if scenario == "truncated" {
						length++
					}
					w.Header().Set("Content-Length", strconv.Itoa(length))
					checksum := fmt.Sprintf("%x", sha256.Sum256(body))
					if scenario == "checksum" {
						checksum = "wrong"
					}
					w.Header().Set("X-Artifact-SHA256", checksum)
					if _, err := w.Write(body); err != nil {
						t.Error(err)
					}
				},
			)
			client := testClient(t, server.URL, identity.Fingerprint.String())
			client.sleep = func(context.Context, time.Duration) error { return context.Canceled }
			if scenario == "missing-directory" {
				if err := os.Remove(
					filepath.Join(client.StateDir(), "staging"),
				); err != nil {
					t.Fatal(err)
				}
			}
			file, err := client.Staging(
				t.Context(),
				agentproto.PollResponse{DeploymentID: 42},
				nil,
			)
			if err == nil || file != nil {
				t.Fatalf("invalid download returned file=%v err=%v", file, err)
			}
			if scenario == "checksum" || scenario == "archive" {
				if !errors.Is(err, staging.ErrInvalid) {
					t.Fatalf("invalid archive error: %v", err)
				}
			}
			entries, readErr := os.ReadDir(
				filepath.Join(client.StateDir(), "staging"),
			)
			if scenario != "missing-directory" &&
				(readErr != nil || len(entries) != 0) {
				t.Fatalf(
					"failed download retained files: %v %v",
					entries,
					readErr,
				)
			}
		})
	}
}

func TestStagingUploadRejectsInvalidSourcesAndServerFailure(t *testing.T) {
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusConflict)
		},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	for _, scenario := range []string{"conflict", "closed", "oversized", "cancelled", "invalid-url"} {
		t.Run(scenario, func(t *testing.T) {
			file, err := os.CreateTemp(t.TempDir(), "upload")
			if err != nil {
				t.Fatal(err)
			}
			if scenario != "closed" {
				t.Cleanup(func() {
					if err := file.Close(); err != nil {
						t.Error(err)
					}
				})
			}
			if scenario == "closed" {
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "oversized" {
				if err := file.Truncate(staging.MaxArchive + 1); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			if scenario == "invalid-url" {
				client.serverURL = "://"
				defer func() { client.serverURL = server.URL }()
			}
			if _, err := client.Staging(
				ctx,
				agentproto.PollResponse{DeploymentID: 42},
				file,
			); err == nil {
				t.Fatal("accepted invalid upload")
			}
		})
	}
}
