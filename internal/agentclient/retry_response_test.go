package agentclient

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClient_retries_malformed_success_response(t *testing.T) {
	serverIdentity := testIdentity(t)
	calls := 0
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusOK)
				return
			}
			_ = json.NewEncoder(w).Encode(agentproto.PollResponse{
				DeploymentID: 42, Payload: "ciphertext", ClaimToken: "claim-secret",
			})
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	client.jitter = func(limit int64) (int64, error) { return limit - 1, nil }

	response, err := client.Poll(context.Background())

	if err != nil || calls != 2 || len(delays) != 1 ||
		response == nil || response.DeploymentID != 42 {
		t.Fatalf(
			"response=%+v err=%v calls=%d delays=%v",
			response,
			err,
			calls,
			delays,
		)
	}
}

func TestClient_retries_server_error(t *testing.T) {
	serverIdentity := testIdentity(t)
	calls := 0
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, _ *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}

	response, err := client.Poll(context.Background())

	if err != nil || response != nil || calls != 2 || len(delays) != 1 {
		t.Fatalf(
			"response=%+v err=%v calls=%d delays=%v",
			response,
			err,
			calls,
			delays,
		)
	}
}
