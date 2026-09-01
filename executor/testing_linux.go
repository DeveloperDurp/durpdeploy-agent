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
		clearCapabilitiesFn: func(*exec.Cmd, bool) error { return nil },
		createCgroupFn:      func(int64) (*cgroup, error) { return &cgroup{}, nil },
		configureCgroupFn:   func(*exec.Cmd, *cgroup) error { return nil },
		removeCgroupFn:      func(*cgroup) error { return nil },
		setupChrootFn:       func(string) (bool, error) { return false, nil },
	}}
}
