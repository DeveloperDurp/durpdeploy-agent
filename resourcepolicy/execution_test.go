package resourcepolicy_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestExecutionPolicyStepBoundaries(t *testing.T) {
	policy := resourcepolicy.Defaults().Execution
	for _, test := range []struct {
		name    string
		timeout time.Duration
		retries int64
		valid   bool
	}{
		{"defaults", 0, 0, true},
		{"maximum", 24 * time.Hour, 10, true},
		{"fractional duration", time.Nanosecond, 0, true},
		{"negative duration", -time.Nanosecond, 0, false},
		{"negative retries", 0, -1, false},
		{"duration exceeded", 24*time.Hour + time.Nanosecond, 0, false},
		{"retries exceeded", 0, 11, false},
		{"largest duration", time.Duration(math.MaxInt64), 0, false},
		{"largest retries", 0, math.MaxInt64, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := policy.ValidateStep(test.timeout, test.retries)
			if (err == nil) != test.valid || (!test.valid && !errors.Is(err, resourcepolicy.ErrExecutionLimits)) {
				t.Fatalf("valid=%t error=%v", test.valid, err)
			}
		})
	}
}

func TestExecutionPolicyValidationAndCustomValues(t *testing.T) {
	for _, policy := range []resourcepolicy.Execution{
		{},
		{MaxRetries: -1, MaxTimeoutSeconds: 10, DefaultTimeoutSeconds: 1},
		{MaxRetries: 1, MaxTimeoutSeconds: 1, DefaultTimeoutSeconds: 2},
		{MaxRetries: 1, MaxTimeoutSeconds: math.MaxInt64, DefaultTimeoutSeconds: 1},
		{MaxRetries: int64(^uint(0) >> 1), MaxTimeoutSeconds: 10, DefaultTimeoutSeconds: 1},
	} {
		if err := policy.ValidateStep(0, 0); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
	for _, policy := range []resourcepolicy.Execution{
		{MaxRetries: 0, MaxTimeoutSeconds: 1, DefaultTimeoutSeconds: 1},
		{MaxRetries: 12, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 600},
	} {
		if err := policy.ValidateStep(time.Duration(policy.MaxTimeoutSeconds)*time.Second, policy.MaxRetries); err != nil {
			t.Fatalf("custom policy rejected: %v", err)
		}
	}
}
