//go:build linux

package executor

import (
	"os"
	"os/exec"
)

// NewExecutorForTest creates an unsandboxed executor for integration tests.
// Production callers must use NewExecutor.
func NewExecutorForTest() *Executor {
	return &Executor{sandbox: &Sandbox{
		uid:                 uint32(os.Getuid()),
		gid:                 uint32(os.Getgid()),
		enabled:             true,
		applyCredentialFn:   func(*exec.Cmd) {},
		clearCapabilitiesFn: func(*exec.Cmd) error { return nil },
	}}
}
