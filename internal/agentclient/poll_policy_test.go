package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestPollPolicyMatchingSnapshots(t *testing.T) {
	for _, retries := range []int64{0, 10, 12} {
		policy := resourcepolicy.Defaults()
		policy.Execution.MaxRetries = retries
		for _, status := range []int{http.StatusOK, http.StatusNoContent} {
			t.Run(fmt.Sprintf("retries-%d/status-%d", retries, status), func(t *testing.T) {
				checkPollPolicyResponse(t, policy, []string{testPolicyAdvertisement(t, policy)}, status, false)
			})
		}
	}
}

func TestPollPolicyLegacyDefaultsAndRejections(t *testing.T) {
	defaults := resourcepolicy.Defaults()
	custom := defaults
	custom.Execution.MaxRetries = 12
	valid := testPolicyAdvertisement(t, defaults)
	for _, status := range []int{http.StatusOK, http.StatusNoContent} {
		cases := []struct {
			name   string
			policy resourcepolicy.Limits
			values []string
			reject bool
		}{
			{"legacy-default", defaults, nil, false},
			{"legacy-custom", custom, nil, true},
			{"mismatch-default", defaults, []string{testPolicyAdvertisement(t, custom)}, true},
			{"mismatch-custom", custom, []string{valid}, true},
			{"repeated", defaults, []string{valid, valid}, true},
			{"combined", defaults, []string{valid + "," + valid}, true},
			{"empty", defaults, []string{""}, true},
			{"unknown", defaults, []string{"resource-policy/2"}, true},
			{"untrusted", defaults, []string{"operator-secret"}, true},
		}
		for _, test := range cases {
			t.Run(fmt.Sprintf("%s/status-%d", test.name, status), func(t *testing.T) {
				checkPollPolicyResponse(t, test.policy, test.values, status, test.reject)
			})
		}
	}
}

func checkPollPolicyResponse(t *testing.T, policy resourcepolicy.Limits, values []string, status int, reject bool) {
	t.Helper()
	expected := testPolicyAdvertisement(t, policy)
	identity := testIdentity(t)
	var requests atomic.Int32
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		checkPollPolicyRequest(t, r, expected)
		for _, value := range values {
			w.Header().Add(resourcepolicy.CapabilityHeader, value)
		}
		w.WriteHeader(status)
		if status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(agentproto.PollResponse{DeploymentID: 42, Payload: "ciphertext", ClaimToken: "claim-secret"})
		}
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	policy.Execution.MaxRetries++
	snapshot := client.ResourcePolicy()
	snapshot.Execution.MaxRetries++
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES", "2")
	probe := &pollPolicyProbe{RoundTripper: client.http.Transport}
	client.http.Transport = probe
	claim, err := client.Poll(t.Context())
	if requests.Load() != 1 || !probe.closed.Load() {
		t.Fatalf("requests=%d response closed=%v", requests.Load(), probe.closed.Load())
	}
	if reject {
		if claim != nil || !errors.Is(err, resourcepolicy.ErrIncompatible) || probe.read.Load() || strings.Contains(err.Error(), "operator-secret") {
			t.Fatalf("incompatible claim delivered/read or diagnostic retained input: claim=%v err=%v read=%v", claim, err, probe.read.Load())
		}
		return
	}
	if err != nil || (claim != nil) != (status == http.StatusOK) {
		t.Fatalf("compatible poll: claim=%v err=%v status=%d", claim, err, status)
	}
	if claim != nil && (claim.DeploymentID != 42 || claim.ClaimToken != "claim-secret") {
		t.Fatal("compatible claim changed")
	}
}

func checkPollPolicyRequest(t *testing.T, request *http.Request, expected string) {
	t.Helper()
	if len(request.TLS.PeerCertificates) != 1 || request.Header.Get(resourcepolicy.CapabilityHeader) != expected || len(request.Header.Values(resourcepolicy.CapabilityHeader)) != 1 {
		t.Error("missing client certificate or incorrect captured policy advertisement")
	}
	if request.Header.Get("X-DurpDeploy-Staging") != "staging/1" || request.Header.Get(artifact.CapabilityHeader) != artifact.Capability {
		t.Error("existing transfer capabilities changed")
	}
	if _, err := agentproto.DecodeRequest[agentproto.PollRequest](request.Body); err != nil {
		t.Errorf("strict poll body changed: %v", err)
	}
}

func TestPollPolicyPreservesRetriesAndOtherRequests(t *testing.T) {
	identity := testIdentity(t)
	policy := resourcepolicy.Defaults()
	policy.Execution.MaxRetries = 12
	expected := testPolicyAdvertisement(t, policy)
	var polls atomic.Int32
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != agentproto.PollPath {
			if r.Header.Get(resourcepolicy.CapabilityHeader) != "" || r.Header.Get(artifact.CapabilityHeader) != "" {
				t.Error("poll capabilities leaked to lifecycle request")
			}
			w.Header().Set(resourcepolicy.CapabilityHeader, "operator-secret")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		checkPollPolicyRequest(t, r, expected)
		w.Header().Set(resourcepolicy.CapabilityHeader, "operator-secret")
		if polls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set(resourcepolicy.CapabilityHeader, expected)
		w.WriteHeader(http.StatusNoContent)
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error { delays = append(delays, delay); return nil }
	client.jitter = func(int64) (int64, error) { return 0, nil }
	if claim, err := client.Poll(t.Context()); err != nil || claim != nil || polls.Load() != 2 || len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("retry behavior changed: claim=%v err=%v polls=%d delays=%v", claim, err, polls.Load(), delays)
	}
	if err := client.Start(t.Context(), 42, "claim-secret"); err != nil {
		t.Fatal(err)
	}
}

func TestPollPolicyRejectsInvalidBeforeRequest(t *testing.T) {
	identity := testIdentity(t)
	var requests atomic.Int32
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(http.StatusNoContent) })
	client := testClient(t, server.URL, identity.Fingerprint.String())
	invalid := resourcepolicy.Defaults()
	invalid.Container.PIDs = 0
	client.resourcePolicy = &invalid
	if claim, err := client.Poll(t.Context()); err == nil || claim != nil || requests.Load() != 0 {
		t.Fatalf("invalid policy reached poll: claim=%v err=%v requests=%d", claim, err, requests.Load())
	}
}

func testPolicyAdvertisement(t *testing.T, policy resourcepolicy.Limits) string {
	t.Helper()
	value, err := policy.Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type pollPolicyProbe struct {
	http.RoundTripper
	read, closed atomic.Bool
}

func (p *pollPolicyProbe) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := p.RoundTripper.RoundTrip(request)
	if err == nil {
		response.Body = &pollPolicyBody{ReadCloser: response.Body, probe: p}
	}
	return response, err
}

type pollPolicyBody struct {
	io.ReadCloser
	probe *pollPolicyProbe
}

func (b *pollPolicyBody) Read(p []byte) (int, error) {
	b.probe.read.Store(true)
	return b.ReadCloser.Read(p)
}
func (b *pollPolicyBody) Close() error { b.probe.closed.Store(true); return b.ReadCloser.Close() }
