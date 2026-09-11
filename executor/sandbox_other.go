//go:build !linux

package executor

import (
	"fmt"
	"os/exec"
)

// runnerUsername mirrors sandbox_linux.go's constant so runner.go can
// reference it regardless of platform (used for the USER/LOGNAME env vars).
const runnerUsername = "durpdeploy-runner"

// Sandbox is unavailable on non-Linux platforms because the runner credential
// boundary uses Linux process attributes.
type Sandbox struct{}

func newSandbox() (*Sandbox, error) {
	return nil, fmt.Errorf("runner sandbox requires Linux")
}

func (s *Sandbox) applyCredential(cmd *exec.Cmd) {}

func (s *Sandbox) clearCapabilities(cmd *exec.Cmd) error {
	return nil
}
