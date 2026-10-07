package agentclient

import (
	"errors"
	"net/http"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestTerminalReport_preserves_protocol_and_lifecycle_endpoint(
	t *testing.T,
) {
	for _, version := range []agentproto.ProtocolVersion{agentproto.AgentV2, agentproto.AgentV3} {
		for _, cancelled := range []bool{false, true} {
			t.Run(
				string(
					version,
				)+map[bool]string{false: "/result", true: "/cancelled"}[cancelled],
				func(t *testing.T) {
					// Given
					identity := testIdentity(t)
					calls := 0
					server := newTLSServer(
						t,
						identity,
						func(w http.ResponseWriter, r *http.Request) {
							calls++
							if cancelled {
								request, err := agentproto.DecodeRequest[agentproto.CancelledRequest](
									r.Body,
								)
								if err != nil || request.Protocol != version ||
									request.ClaimToken != "claim-secret" ||
									r.URL.Path != "/agent/v1/deployments/42/cancelled" {
									t.Errorf(
										"cancelled replay: %+v, %v, %s",
										request,
										err,
										r.URL.Path,
									)
								}
							} else {
								request, err := agentproto.DecodeRequest[agentproto.ResultRequest](
									r.Body,
								)
								if err != nil || request.Protocol != version ||
									request.ClaimToken != "claim-secret" ||
									request.State != agentproto.ResultSucceeded ||
									r.URL.Path != "/agent/v1/deployments/42/result" {
									t.Errorf(
										"result replay: %+v, %v, %s",
										request,
										err,
										r.URL.Path,
									)
								}
							}
							w.WriteHeader(http.StatusNoContent)
						},
					)
					client := testClient(
						t,
						server.URL,
						identity.Fingerprint.String(),
					)
					client.protocol = version
					var request agentproto.Request = agentproto.ResultRequest{ClaimToken: "claim-secret", State: agentproto.ResultSucceeded}
					if cancelled {
						request = agentproto.CancelledRequest{
							ClaimToken: "claim-secret",
						}
					}
					sealed, err := client.SealTerminalReport(42, request)
					if err != nil {
						t.Fatal(err)
					}
					restarted, err := NewPaired(client.StateDir(), "test")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(restarted.Close)
					// When
					saved, err := restarted.DecodeTerminalReport(42, sealed)
					if err != nil {
						t.Fatal(err)
					}
					if err := restarted.ReportTerminal(
						t.Context(),
						42,
						saved,
					); err != nil {
						t.Fatal(err)
					}
					// Then
					if calls != 1 {
						t.Fatalf("requests: %d", calls)
					}
					restarted.serverURL = "https://another-pairing.test"
					if _, err := restarted.DecodeTerminalReport(
						42,
						sealed,
					); err == nil {
						t.Fatal("different pairing accepted")
					}
					if _, err := client.DecodeTerminalReport(
						43,
						sealed,
					); err == nil {
						t.Fatal("different deployment accepted")
					}
				},
			)
		}
	}
}

func TestTerminalReport_rejects_uncertain_or_conflicting_recovery(
	t *testing.T,
) {
	client := testClient(
		t,
		"https://server.test",
		testIdentity(t).Fingerprint.String(),
	)
	cleanup, err := client.SealCleanupReport(
		agentproto.PollResponse{DeploymentID: 42, ClaimToken: "claim-secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DecodeTerminalReport(42, cleanup); err == nil {
		t.Fatal("cleanup uncertainty accepted without reconciliation")
	}
	conflicting, err := client.sealReport(42, terminalReport{
		Result: &agentproto.ResultRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			ClaimToken: "claim-secret",
			State:      agentproto.ResultSucceeded,
		},
		Cancelled: &agentproto.CancelledRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			ClaimToken: "claim-secret",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DecodeTerminalReport(42, conflicting); err == nil {
		t.Fatal("conflicting lifecycle variants accepted")
	}
}

func TestTerminalReport_generic_conflict_is_not_acknowledgement(t *testing.T) {
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) },
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	sealed, err := client.SealTerminalReport(
		42,
		agentproto.ResultRequest{
			ClaimToken: "claim-secret",
			State:      agentproto.ResultSucceeded,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := client.DecodeTerminalReport(42, sealed)
	if err != nil {
		t.Fatal(err)
	}
	var statusErr *StatusError
	if err := client.ReportTerminal(
		t.Context(),
		42,
		saved,
	); !errors.As(err, &statusErr) ||
		statusErr.Status != http.StatusConflict {
		t.Fatalf("generic409 treated as ACK: %v", err)
	}
}
