package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestResultDiagnostic_bounds_and_excludes_untrusted_error_text(t *testing.T) {
	private := "claim-private variable-private password=credential-private raw-script-output\n"
	untrusted := errors.New(strings.Repeat(private, 32768))
	transport := &url.Error{Op: "POST", URL: "https://user:credential-private@server/claim-private", Err: untrusted}
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"success", nil, ""},
		{"execution", untrusted, executionFailureDiagnostic},
		{"transport", transport, executionFailureDiagnostic},
		{"transport-deadline", context.DeadlineExceeded, executionFailureDiagnostic},
		{"timeout", fmt.Errorf("%s: %w", private, runner.ErrStepTimeout), stepTimeoutDiagnostic},
		{"logs", errors.Join(runner.ErrLogDelivery, transport), logDeliveryFailureDiagnostic},
		{"log-deadline", errors.Join(runner.ErrLogDelivery, context.DeadlineExceeded), logDeliveryFailureDiagnostic},
		{"logs-and-timeout", errors.Join(runner.ErrStepTimeout, runner.ErrLogDelivery, untrusted), logDeliveryFailureDiagnostic},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := resultDiagnostic(test.err)
			if got != test.want || len(got) > maxResultDiagnosticBytes || !utf8.ValidString(got) {
				t.Fatalf("diagnostic = %q (%d bytes)", got, len(got))
			}
			for _, value := range strings.Fields(private) {
				if strings.Contains(got, value) {
					t.Fatalf("private value in diagnostic: %q", got)
				}
			}
		})
	}
}

func TestResultDiagnostic_wire_contract_accepts_existing_protocols(t *testing.T) {
	for _, version := range []agentproto.ProtocolVersion{agentproto.AgentV1, agentproto.AgentV2, agentproto.AgentV3} {
		for _, diagnostic := range []string{executionFailureDiagnostic, stepTimeoutDiagnostic, logDeliveryFailureDiagnostic, interruptedExecutionDiagnostic} {
			if len(diagnostic) > maxResultDiagnosticBytes {
				t.Fatalf("diagnostic exceeds budget: %d", len(diagnostic))
			}
			request := agentproto.ResultRequest{
				ProtocolEnvelope: agentproto.ProtocolEnvelope{Protocol: version},
				ClaimToken:       "claim-private",
				State:            agentproto.ResultFailed,
				Error:            diagnostic,
			}
			raw, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			got, err := agentproto.DecodeRequest[agentproto.ResultRequest](bytes.NewReader(raw))
			if err != nil || got != request {
				t.Fatalf("%s result failed strict decoding: %+v, %v", version, got, err)
			}
		}
	}
}

func TestResultDiagnostics_report_execution_timeout_and_log_failures(t *testing.T) {
	for _, test := range []struct {
		name    string
		marker  string
		logFail bool
		want    string
	}{
		{"success", "", false, ""},
		{"execution", "run-fail", false, executionFailureDiagnostic},
		{"timeout", "run-cancel", false, stepTimeoutDiagnostic},
		{"final-log-flush", "", true, logDeliveryFailureDiagnostic},
		{"streaming-logs", "run-output", true, logDeliveryFailureDiagnostic},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newAgentSubprocessFixture(t, "exit 0")
			client, directory := claimLifecycleClient(t, fixture)
			fixture.payload.Release.Steps[0].Name = "password=subprocess-secret token=test-claim plain-value"
			fixture.payload.Release.Steps[0].TimeoutSeconds = 1
			fixture.addVariable(variablePayload{Name: "PLAIN", Value: "plain-value"})
			if test.marker != "" {
				if err := os.WriteFile(filepath.Join(directory, test.marker), nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if test.logFail {
				fixture.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/logs") {
						w.WriteHeader(http.StatusBadRequest)
						_, _ = w.Write([]byte("password=subprocess-secret test-claim plain-value"))
						return
					}
					fixture.handle(w, r)
				})
			}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			claim, err := client.Poll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := executeClaim(ctx, client, *claim); err != nil {
				t.Fatal(err)
			}
			result := <-fixture.result
			wantState := agentproto.ResultFailed
			if test.want == "" {
				wantState = agentproto.ResultSucceeded
			}
			if result.State != wantState || result.Error != test.want {
				t.Fatalf("result state=%s error=%q", result.State, result.Error)
			}
			t.Logf("POST /agent/v1/deployments/42/result acknowledged: protocol=%s state=%s error=%q", result.Protocol, result.State, result.Error)
		})
	}
}
