package main

import (
	"testing"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
)

func TestDecodePayload_acceptsOneBasedStepOrder(t *testing.T) {
	// Given
	raw := []byte(
		`{"deployment_id":42,"release":{"id":1,"project_id":1,"version":"v1","steps":[{"name":"deploy","script_body":"echo ok","sort_order":1,"timeout_seconds":0,"max_retries":0}]},"environment":{"id":1,"name":"test"},"variables":[]}`,
	)

	// When
	payload, err := decodePayload(raw, 42)

	// Then
	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if got := payload.Release.Steps[0].SortOrder; got != 1 {
		t.Fatalf("sort_order = %d", got)
	}
	if got := payload.Release.Steps[0].Interpreter; got != runner.InterpreterBash {
		t.Fatalf("interpreter = %q, want bash", got)
	}
}

func TestDecodePayload_accepts_fixed_nonBash_interpreter(t *testing.T) {
	raw := []byte(
		`{"deployment_id":42,"release":{"id":1,"project_id":1,"version":"v1","steps":[{"name":"deploy","script_body":"print('ok')","interpreter":"python3","sort_order":1,"timeout_seconds":0,"max_retries":0}]},"environment":{"id":1,"name":"test"},"variables":[]}`,
	)

	payload, err := decodePayload(raw, 42)

	if err != nil {
		t.Fatalf("valid payload rejected: %v", err)
	}
	if got := payload.Release.Steps[0].Interpreter; got != runner.InterpreterPython3 {
		t.Fatalf("interpreter = %q, want python3", got)
	}
}

func TestDecodePayload_rejects_unknown_interpreter(t *testing.T) {
	raw := []byte(
		`{"deployment_id":42,"release":{"id":1,"project_id":1,"version":"v1","steps":[{"name":"deploy","script_body":"echo ok","interpreter":"/bin/bash","sort_order":1,"timeout_seconds":0,"max_retries":0}]},"environment":{"id":1,"name":"test"},"variables":[]}`,
	)

	_, err := decodePayload(raw, 42)

	if err == nil {
		t.Fatal("unknown interpreter accepted")
	}
}

func TestDeploymentPayload_rejects_unavailable_interpreter(t *testing.T) {
	payload := deploymentPayload{Release: releasePayload{Steps: []runner.Step{
		{Name: "deploy", Interpreter: runner.InterpreterPwsh},
	}}}

	err := payload.validateInterpreters(func(runner.Interpreter) bool {
		return false
	})

	if err == nil {
		t.Fatal("unavailable interpreter accepted")
	}
}

func TestDecodePayload_acceptsServerSerializedMultiStepOrder(t *testing.T) {
	// Given
	raw := []byte(`{
		"deployment_id":42,
		"release":{
			"id":1,
			"project_id":1,
			"version":"v1",
			"steps":[
				{"name":"first","script_body":"echo first","sort_order":1,"timeout_seconds":0,"max_retries":0},
				{"name":"second","script_body":"echo second","sort_order":2,"timeout_seconds":5,"max_retries":1}
			]
		},
		"environment":{"id":1,"name":"test"},
		"variables":[]
	}`)

	// When
	payload, err := decodePayload(raw, 42)

	// Then
	if err != nil {
		t.Fatalf("valid server-shaped payload rejected: %v", err)
	}
	if got := len(payload.Release.Steps); got != 2 {
		t.Fatalf("step count = %d", got)
	}
}

func TestDecodePayload_rejectsGappedStepOrder(t *testing.T) {
	// Given
	raw := []byte(`{
		"deployment_id":42,
		"release":{
			"id":1,
			"project_id":1,
			"version":"v1",
			"steps":[
				{"name":"first","script_body":"echo first","sort_order":10,"timeout_seconds":0,"max_retries":0},
				{"name":"second","script_body":"echo second","sort_order":30,"timeout_seconds":0,"max_retries":0}
			]
		},
		"environment":{"id":1,"name":"test"},
		"variables":[]
	}`)

	// When
	_, err := decodePayload(raw, 42)

	// Then
	if err == nil {
		t.Fatal("gapped step order accepted")
	}
}

func TestDecodePayload_rejectsDeploymentMismatch(t *testing.T) {
	// Given
	raw := []byte(
		`{"deployment_id":41,"release":{"id":1,"project_id":1,"version":"v1","steps":[]},"environment":{"id":1,"name":"test"},"variables":[]}`,
	)

	// When
	_, err := decodePayload(raw, 42)

	// Then
	if err == nil {
		t.Fatal("mismatched payload accepted")
	}
}

func TestDecodePayload_acceptsDeduplicatedVariables(t *testing.T) {
	// Given
	raw := []byte(
		`{"deployment_id":42,"release":{"id":1,"project_id":1,"version":"v1","steps":[]},"environment":{"id":1,"name":"test"},"variables":[{"name":"REGION","value":"resolved","secret":false}]}`,
	)

	// When
	payload, err := decodePayload(raw, 42)
	var environment map[string]string
	if err == nil {
		environment, _, err = payload.environment()
	}

	// Then
	if err != nil {
		t.Fatalf("deduplicated payload rejected: %v", err)
	}
	if len(environment) != 1 || environment["REGION"] != "resolved" {
		t.Fatalf("environment = %#v, want one resolved REGION", environment)
	}
}

func TestDecodePayload_rejectsDuplicateVariablesInEnvironment(t *testing.T) {
	// Given
	raw := []byte(
		`{"deployment_id":42,"release":{"id":1,"project_id":1,"version":"v1","steps":[]},"environment":{"id":1,"name":"test"},"variables":[{"name":"REGION","value":"first","secret":false},{"name":"REGION","value":"last","secret":false}]}`,
	)

	// When
	payload, err := decodePayload(raw, 42)
	if err == nil {
		_, _, err = payload.environment()
	}

	// Then
	if err == nil {
		t.Fatal("duplicate variable payload accepted")
	}
}
