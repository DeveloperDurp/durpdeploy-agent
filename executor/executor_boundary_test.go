//go:build linux

package executor

import "testing"

func TestExecutor_Fails_when_boundary_was_not_validated(t *testing.T) {
	// Given
	executor := &Executor{}
	job := NewJob(JobConfig{Name: "unvalidated", ScriptBody: "exit 0"})

	// When
	err := executor.Execute(t.Context(), job, NewCallbacks(CallbacksConfig{}))

	// Then
	if err == nil {
		t.Fatal("zero-value executor ran without boundary validation")
	}
}
