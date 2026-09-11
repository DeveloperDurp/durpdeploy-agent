//go:build linux && agenttest

package executor

import (
	"os"
	"os/exec"
)

// NewExecutorForAgentTest creates an unsandboxed executor for the agent's
// subprocess protocol tests. It is unavailable from ordinary builds.
func NewExecutorForAgentTest() *Executor {
	return &Executor{sandbox: &Sandbox{
		uid:                 uint32(os.Getuid()),
		gid:                 uint32(os.Getgid()),
		enabled:             true,
		applyCredentialFn:   func(*exec.Cmd) {},
		clearCapabilitiesFn: func(*exec.Cmd) error { return nil },
	}}
}
