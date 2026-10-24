package agentclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestArtifactConfiguredDialTimeout(t *testing.T) {
	for _, seconds := range []int64{1, 3} {
		t.Run(strconv.FormatInt(seconds, 10), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				policy := resourcepolicy.Defaults()
				policy.Artifact.DialTimeoutSeconds = seconds
				original := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}}
				client := &Client{http: &http.Client{Transport: original}, resourcePolicy: &policy}
				transport := client.artifactTransport()
				defer transport.CloseIdleConnections()
				request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://example.invalid", nil)
				if err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				_, err = transport.RoundTrip(request)
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) != time.Duration(seconds)*time.Second {
					t.Fatalf("dial exceeded captured budget: %v %v", time.Since(started), err)
				}
				if original.TLSHandshakeTimeout != 0 || original.ResponseHeaderTimeout != 0 {
					t.Fatal("artifact timeout changed the control-plane transport")
				}
			})
		})
	}
}

func TestArtifactConfiguredNetworkTimeouts(t *testing.T) {
	for _, phase := range []string{"headers", "tls"} {
		t.Run(phase, func(t *testing.T) {
			identity := testIdentity(t)
			data := []byte("pinned package")
			fixture := artifactResponseFixture{data: data, pin: testArtifactPin(data)}
			server := httptest.NewUnstartedServer(artifactResponseHandler(t, fixture))
			server.TLS = &tls.Config{
				Certificates: []tls.Certificate{identity.Certificate}, MinVersion: tls.VersionTLS13,
				ClientAuth: tls.RequireAnyClientCert,
			}
			if phase == "tls" {
				server.TLS.GetConfigForClient = func(*tls.ClientHelloInfo) (*tls.Config, error) {
					time.Sleep(1500 * time.Millisecond)
					return nil, nil
				}
			} else {
				handler := server.Config.Handler
				server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-time.After(1500 * time.Millisecond):
						handler.ServeHTTP(w, r)
					case <-r.Context().Done():
					}
				})
			}
			server.StartTLS()
			defer server.Close()
			legacy := testClient(t, server.URL, identity.Fingerprint.String())
			for _, seconds := range []int64{1, 5} {
				t.Run(strconv.FormatInt(seconds, 10), func(t *testing.T) {
					checkArtifactNetworkBudget(t, legacy, seconds, fixture)
				})
			}
		})
	}
}

func checkArtifactNetworkBudget(t *testing.T, legacy *Client, seconds int64, fixture artifactResponseFixture) {
	t.Helper()
	policy := resourcepolicy.Defaults()
	policy.Artifact.HeaderTimeoutSeconds = seconds
	policy.Artifact.TLSTimeoutSeconds = seconds
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "test", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	policy.Artifact.HeaderTimeoutSeconds = 1
	policy.Artifact.TLSTimeoutSeconds = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_ARTIFACT_TLS_TIMEOUT_SECONDS", "1")
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_ARTIFACT_HEADER_TIMEOUT_SECONDS", "1")
	var waits int
	client.sleep = func(context.Context, time.Duration) error { waits++; return context.Canceled }
	file, err := client.Artifact(t.Context(), agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim"}, fixture.pin)
	if seconds == 1 {
		if file != nil || !errors.Is(err, context.Canceled) || waits != 1 {
			t.Fatalf("short network budget did not reject: %v %v waits=%d", file, err, waits)
		}
		assertTransferStagingEmpty(t, client)
	} else {
		assertArtifactDownload(t, artifactDownloadResult{file, err}, fixture.data)
	}
}

type artifactWaitTransport struct{ requests *atomic.Int32 }

func (transport artifactWaitTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.requests.Add(1)
	<-request.Context().Done()
	return nil, request.Context().Err()
}

func TestArtifactFetchDeadlinePreservesCallerBudget(t *testing.T) {
	for _, earlierCaller := range []bool{false, true} {
		t.Run(strconv.FormatBool(earlierCaller), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				policy := resourcepolicy.Defaults()
				policy.Artifact.FetchTimeoutSeconds = 3
				var requests atomic.Int32
				client := &Client{
					http:           &http.Client{Transport: artifactWaitTransport{&requests}},
					resourcePolicy: &policy, sleep: sleep,
					jitter: func(int64) (int64, error) { return 0, nil },
				}
				ctx := t.Context()
				want := 3 * time.Second
				if earlierCaller {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, time.Second)
					defer cancel()
					want = time.Second
				}
				started := time.Now()
				file, err := client.Artifact(ctx, agentproto.PollResponse{}, testArtifactPin([]byte("package")))
				if file != nil || !errors.Is(err, context.DeadlineExceeded) || requests.Load() != 1 || time.Since(started) != want {
					t.Fatalf("fetch deadline changed: file=%v err=%v requests=%d elapsed=%v", file, err, requests.Load(), time.Since(started))
				}
			})
		})
	}
}

func assertTransferStagingEmpty(t *testing.T, client *Client) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(client.StateDir(), "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed transfer retained spools: %v %v", entries, err)
	}
}
