package main

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestPayloadExecutionPolicyBeforeConversions(t *testing.T) {
	defaults := resourcepolicy.Defaults().Execution
	raised := resourcepolicy.Execution{MaxRetries: 12, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 600}
	lowered := resourcepolicy.Execution{MaxRetries: 0, MaxTimeoutSeconds: 1, DefaultTimeoutSeconds: 1}
	for _, test := range []struct {
		name             string
		policy           resourcepolicy.Execution
		timeout, retries int64
		valid            bool
	}{
		{"default maximum", defaults, 86400, 10, true},
		{"default retries exceeded", defaults, 0, 11, false},
		{"default timeout exceeded", defaults, 86401, 0, false},
		{"raised maximum", raised, 172800, 12, true},
		{"lowered zero meanings", lowered, 0, 0, true},
		{"lowered timeout exceeded", lowered, 2, 0, false},
		{"lowered retries exceeded", lowered, 0, 1, false},
		{"negative timeout", defaults, -1, 0, false},
		{"negative retries", defaults, 0, -1, false},
		{"duration overflow", defaults, math.MaxInt64, 0, false},
		{"retry overflow", defaults, 0, math.MaxInt64, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(deploymentPayload{DeploymentID: 42, Release: releasePayload{Steps: []runner.Step{{
				Name: "valid", ScriptBody: "exit 0", SortOrder: 1, TimeoutSeconds: test.timeout, MaxRetries: test.retries,
			}}}})
			if err != nil {
				t.Fatal(err)
			}
			policy := resourcepolicy.Defaults()
			policy.Execution = test.policy
			_, err = decodePayloadWithResourcePolicy(raw, 42, policy)
			if (err == nil) != test.valid || (!test.valid && !errors.Is(err, resourcepolicy.ErrExecutionLimits)) {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}
