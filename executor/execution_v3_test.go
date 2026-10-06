package executor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStep_legacy_serialization_refuses_lossy_downgrades(t *testing.T) {
	// Given
	steps := []Step{
		{Interpreter: InterpreterBash, ExecutionMode: agentproto.ExecutionHost},
		{Interpreter: InterpreterPython3},
		{
			Interpreter:    InterpreterBash,
			ExecutionMode:  agentproto.ExecutionContainer,
			ContainerImage: "test/image",
		},
		{Interpreter: InterpreterBash, VariableNames: []string{"REGION"}},
	}
	for index, step := range steps {
		for _, protocol := range []agentproto.ProtocolVersion{agentproto.AgentV1, agentproto.AgentV2, agentproto.AgentV3} {
			t.Run(
				string(protocol)+"/"+string(rune('a'+index)),
				func(t *testing.T) {
					// When
					raw, err := step.MarshalForProtocol(protocol)
					// Then
					reject := protocol != agentproto.AgentV3 &&
						(index >= 2 || index == 1 && protocol == agentproto.AgentV1)
					if reject {
						if !errors.Is(err, agentproto.ErrUnsupportedProtocol) {
							t.Fatalf("downgrade: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if protocol != agentproto.AgentV3 &&
						(strings.Contains(string(raw), "execution_mode") || strings.Contains(string(raw), "container_image") || strings.Contains(string(raw), "variable_names")) {
						t.Fatalf("legacy fields: %s", raw)
					}
				},
			)
		}
	}
}

func TestStep_rejects_contradictory_container_fields(t *testing.T) {
	// Given
	steps := []Step{
		{ExecutionMode: agentproto.ExecutionHost, ContainerImage: "image"},
		{ExecutionMode: agentproto.ExecutionContainer},
		{
			ExecutionMode:  agentproto.ExecutionContainer,
			ContainerImage: "-unsafe",
		},
		{
			ExecutionMode:  agentproto.ExecutionContainer,
			ContainerImage: "image",
			VariableNames:  []string{"HOME"},
		},
		{VariableNames: []string{"REGION", "REGION"}},
	}
	for _, step := range steps {
		// When
		_, err := json.Marshal(step)
		// Then
		if err == nil {
			t.Fatalf("accepted invalid step: %+v", step)
		}
	}
}

func TestJob_clones_variable_selection_and_environment(t *testing.T) {
	// Given
	names := []string{"REGION"}
	env := map[string]string{"REGION": "west", "OTHER": "east"}
	job := NewJob(JobConfig{VariableNames: names, Environment: env})
	names[0] = "OTHER"
	env["REGION"] = "changed"
	// When
	selected, err := selectStepEnvironment(
		job.environment,
		job.variableNames,
		false,
	)
	// Then
	if err != nil || len(selected) != 1 || selected["REGION"] != "west" {
		t.Fatalf("snapshot: %v, %v", selected, err)
	}
}
