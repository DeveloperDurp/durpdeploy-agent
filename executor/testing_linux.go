//go:build linux

package executor

// NewExecutorForTest creates an unsandboxed executor for integration tests.
// Production callers must use NewExecutor.
func NewExecutorForTest() *Executor {
	return &Executor{}
}
