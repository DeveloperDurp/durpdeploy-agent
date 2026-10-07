package agentclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClient_cleanup_report_survives_restart_and_outage(t *testing.T) {
	// Given
	identity := testIdentity(t)
	calls := 0
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			var report agentproto.ResultRequest
			if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
				t.Error(err)
			}
			if report.State != agentproto.ResultCleanupUnconfirmed ||
				report.ClaimToken != "claim-secret" ||
				report.Protocol != agentproto.AgentV3 {
				t.Errorf("invalid report: %+v", report)
			}
			if calls == 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	sealed, err := client.SealCleanupReport(
		agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim-secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	client.sleep = func(ctx context.Context, _ time.Duration) error { <-ctx.Done(); return ctx.Err() }
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	// When
	err = client.ReportCleanup(ctx, 42, sealed)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("outage: %v", err)
	}
	// Restore same durable identity/server; a recreated client can replay the ciphertext.
	restarted, err := NewPaired(client.StateDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	err = restarted.ReportCleanup(t.Context(), 42, sealed)
	// Then
	if err != nil || calls != 2 ||
		strings.Contains(string(sealed), "claim-secret") {
		t.Fatalf("report replay: %v, calls=%d", err, calls)
	}
	restarted.serverURL = "https://other-server.test"
	if err := restarted.ReportCleanup(
		t.Context(),
		42,
		sealed,
	); err == nil ||
		calls != 2 {
		t.Fatal("report replayed to another pairing")
	}
	if err := client.ReportCleanup(
		t.Context(),
		43,
		sealed,
	); err == nil ||
		calls != 2 {
		t.Fatal("report replayed for another deployment")
	}
}
