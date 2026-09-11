//go:build linux

package executor

import "testing"

func TestExecutionBoundary_AllowsServiceMode(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")

	// When
	err := validateExecutionBoundary()

	// Then
	if err != nil {
		t.Fatalf("validate service execution boundary: %v", err)
	}
}

func TestSandbox_FailsClosed_WhenServiceBoundaryMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "")
	// When
	err := validateExecutionBoundary()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without service boundary")
	}
}
