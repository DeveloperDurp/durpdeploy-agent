package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestTerminalConflict_persists_confirmed_cancellation_before_ack(
	t *testing.T,
) {
	for _, state := range []agentproto.ResultState{
		agentproto.ResultFailed, agentproto.ResultSucceeded,
	} {
		t.Run(string(state), func(t *testing.T) {
			testConfirmedTerminalCancellation(t, state)
		})
	}
}

func testConfirmedTerminalCancellation(
	t *testing.T, state agentproto.ResultState,
) {
	fixture := newAgentSubprocessFixture(t, "exit 0")
	client, _ := claimLifecycleClient(t, fixture)
	claim := agentproto.PollResponse{DeploymentID: 42, ClaimToken: "test-claim"}
	if err := persistTerminalResult(client, claim, agentproto.ResultRequest{
		ClaimToken: claim.ClaimToken, State: state,
	}); err != nil {
		t.Fatal(err)
	}
	client.Close()
	fixture.server.Config.Handler = http.HandlerFunc(func(
		w http.ResponseWriter, r *http.Request,
	) {
		switch r.URL.Path {
		case "/agent/v1/deployments/42/result":
			w.WriteHeader(http.StatusConflict)
		case "/agent/v1/deployments/42/heartbeat":
			var heartbeat agentproto.HeartbeatRequest
			if err := json.NewDecoder(r.Body).Decode(&heartbeat); err != nil {
				t.Error(err)
			}
			if heartbeat.ClaimToken != claim.ClaimToken {
				t.Error("cancellation check lost claim authentication")
			}
			_ = json.NewEncoder(w).Encode(agentproto.HeartbeatResponse{
				CancelRequested: true,
				ServerPins: []agentproto.CertificateFingerprint{
					agentproto.CertificateFingerprint(
						fixture.serverID.Fingerprint.String(),
					),
				},
			})
		default:
			fixture.handle(w, r)
		}
	})
	fixture.cancelledStatus = http.StatusBadRequest
	// Restart without container opt-in. Recovery must preserve agent/3.
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var statusErr *agentclient.StatusError
	if err := resumeCleanupResult(
		ctx,
		restarted,
	); !errors.As(
		err,
		&statusErr,
	) ||
		statusErr.Status != http.StatusBadRequest {
		t.Fatalf(
			"confirmed cancellation never reached acknowledgement: %v",
			err,
		)
	}
	raw, err := os.ReadFile(filepath.Join(fixture.stateDir, claimFileName))
	if err != nil {
		t.Fatal(err)
	}
	var marker claimMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatal(err)
	}
	request, err := restarted.DecodeTerminalReport(42, marker.TerminalReport)
	if err != nil {
		t.Fatal(err)
	}
	ack, ok := request.(agentproto.CancelledRequest)
	if !ok || ack.Protocol != agentproto.AgentV3 ||
		ack.ClaimToken != claim.ClaimToken || marker.TerminalAcknowledged {
		t.Fatalf("unacknowledged cancellation was not preserved: %T", request)
	}
	fixture.mu.Lock()
	fixture.cancelledStatus = 0
	fixture.pollServed = true
	fixture.mu.Unlock()
	received := make(chan agentproto.CancelledRequest, 1)
	fixture.cancelledReceived = func(request agentproto.CancelledRequest) {
		received <- request
	}
	done := make(chan error, 1)
	go func() { done <- runPaired(ctx, restarted) }()
	select {
	case <-fixture.pollAgain:
	case <-ctx.Done():
		t.Fatal("polling never resumed after cancellation acknowledgement")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if ack := <-received; ack.Protocol != agentproto.AgentV3 ||
		ack.ClaimToken != claim.ClaimToken {
		t.Fatal("replayed cancellation changed protocol or authentication")
	}
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("acknowledged claim was retained: %v", err)
	}
}

func TestTerminalConflict_retains_result_without_confirmed_cancellation(
	t *testing.T,
) {
	for _, status := range []int{http.StatusOK, http.StatusConflict} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			fixture := newAgentSubprocessFixture(t, "exit 0")
			client, _ := claimLifecycleClient(t, fixture)
			claim := agentproto.PollResponse{
				DeploymentID: 42,
				ClaimToken:   "test-claim",
			}
			if err := persistTerminalResult(
				client,
				claim,
				agentproto.ResultRequest{
					ClaimToken: claim.ClaimToken,
					State:      agentproto.ResultFailed,
				},
			); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(fixture.stateDir, claimFileName)
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			fixture.server.Config.Handler = http.HandlerFunc(func(
				w http.ResponseWriter, r *http.Request,
			) {
				if r.URL.Path != "/agent/v1/deployments/42/heartbeat" {
					if r.URL.Path != "/agent/v1/deployments/42/result" {
						t.Errorf("unexpected request: %s", r.URL.Path)
					}
					w.WriteHeader(http.StatusConflict)
					return
				}
				w.WriteHeader(status)
				if status == http.StatusOK {
					_ = json.NewEncoder(w).Encode(agentproto.HeartbeatResponse{
						ServerPins: []agentproto.CertificateFingerprint{
							agentproto.CertificateFingerprint(
								fixture.serverID.Fingerprint.String(),
							),
						},
					})
				}
			})
			var statusErr *agentclient.StatusError
			if err := resumeCleanupResult(
				t.Context(),
				client,
			); !errors.As(err, &statusErr) ||
				statusErr.Status != http.StatusConflict {
				t.Fatalf("unconfirmed conflict was acknowledged: %v", err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("unconfirmed result marker changed: %v", err)
			}
		})
	}
}
