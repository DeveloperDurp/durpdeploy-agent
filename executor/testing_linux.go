//go:build linux

package executor

import "github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"

// NewExecutorForTest creates an unsandboxed executor for integration tests.
// Production callers must use NewExecutor.
func NewExecutorForTest() *Executor {
	return &Executor{boundaryValidated: true, executionPolicy: resourcepolicy.Defaults().Execution}
}
