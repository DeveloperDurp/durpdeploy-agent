//go:build !unix

package executor

import "os/exec"

// setPgid is a no-op on non-Unix platforms (no process group concept);
// killProcessGroup below correspondingly does nothing.
func setPgid(cmd *exec.Cmd) {}

// killProcessGroup is a no-op on non-Unix platforms. See procgroup_unix.go
// for the Unix implementation.
func killProcessGroup(pgid int) {}

// KillProcessGroup is a no-op on platforms without Unix process groups.
func KillProcessGroup(pgid int) {}
