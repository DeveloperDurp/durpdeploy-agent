package agentclient

import (
	"errors"
	"net/http"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClient_legacy_capabilities_reject_v3_steps(t *testing.T) {
	// Given
	client := &Client{
		protocol:     agentproto.AgentV2,
		interpreters: []executor.Interpreter{executor.InterpreterBash},
	}
	steps := []executor.Step{
		{Interpreter: executor.InterpreterBash},
		{Interpreter: executor.InterpreterPwsh},
		{
			Interpreter:    executor.InterpreterBash,
			ExecutionMode:  agentproto.ExecutionContainer,
			ContainerImage: "image",
		},
		{
			Interpreter:   executor.InterpreterBash,
			VariableNames: []string{"REGION"},
		},
	}
	for index, step := range steps {
		// When
		supported := client.SupportsStep(step)
		// Then
		if supported != (index == 0) {
			t.Fatalf("step %d support = %t", index, supported)
		}
	}
}

func TestClient_v3_rejected_by_legacy_server_without_claiming(t *testing.T) {
	// Given
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) },
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	client.protocol = agentproto.AgentV3
	// When
	claim, err := client.Poll(t.Context())
	// Then
	var status *StatusError
	if claim != nil || !errors.As(err, &status) ||
		status.Status != http.StatusBadRequest {
		t.Fatalf("claim = %v, error = %v", claim, err)
	}
}
