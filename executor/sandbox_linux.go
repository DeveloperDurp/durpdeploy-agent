//go:build linux

package executor

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"syscall"
)

var (
	lookupRunnerUser = user.Lookup
	lookupSetpriv    = exec.LookPath
)

// runnerUsername is the dedicated, low-privileged account (see docs/deploy.md
// Step 5) that step scripts execute as, instead of the durpdeploy service
// user (P1-4). This keeps a compromised/buggy step from reading the SQLite
// DB, the secret key file, or other files only the service user can access.
const runnerUsername = "durpdeploy-runner"

// Sandbox resolves the runner UID/GID once at startup and applies
// the credential and capability boundary to each step command. Filesystem
// and cgroup boundaries are owned by the service manager or container runtime.
type Sandbox struct {
	uid                 uint32
	gid                 uint32
	enabled             bool
	applyCredentialFn   func(*exec.Cmd)
	clearCapabilitiesFn func(*exec.Cmd) error
}

// newSandbox looks up the dedicated durpdeploy-runner account. A missing or
// malformed account is fatal: scripts must never execute as the service user.
func newSandbox() (*Sandbox, error) {
	if os.Getenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY") != "service" {
		return nil, fmt.Errorf(
			"runner sandbox requires service execution boundary",
		)
	}
	s := &Sandbox{}
	u, err := lookupRunnerUser(runnerUsername)
	if err != nil {
		return nil, fmt.Errorf("lookup runner user %q: %w", runnerUsername, err)
	}
	uid, errUID := strconv.ParseUint(u.Uid, 10, 32)
	gid, errGID := strconv.ParseUint(u.Gid, 10, 32)
	if errUID != nil {
		return nil, fmt.Errorf("parse runner UID %q: %w", u.Uid, errUID)
	}
	if errGID != nil {
		return nil, fmt.Errorf("parse runner GID %q: %w", u.Gid, errGID)
	}
	s.uid, s.gid = uint32(uid), uint32(gid)
	s.enabled = true
	return s, nil
}

// applyCredential drops the step process to the durpdeploy-runner UID/GID.
// Preserves any SysProcAttr fields already set (e.g. setPgid's Setpgid).
// NoNewPrivileges is set at the systemd unit level (see
// systemd/durpdeploy.service) since Go's syscall.SysProcAttr does not
// expose a per-Cmd equivalent.
func (s *Sandbox) applyCredential(cmd *exec.Cmd) {
	if s.applyCredentialFn != nil {
		s.applyCredentialFn(cmd)
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{
		Uid:    s.uid,
		Gid:    s.gid,
		Groups: []uint32{s.gid},
	}
}

// clearCapabilities makes setpriv the immediate child and has it remove every
// inherited capability before it execs the attacker-controlled step. Changing
// UID alone is insufficient when the service has ambient capabilities.
func (s *Sandbox) clearCapabilities(cmd *exec.Cmd) error {
	if s.clearCapabilitiesFn != nil {
		return s.clearCapabilitiesFn(cmd)
	}
	if !s.enabled {
		return fmt.Errorf("runner sandbox has no %q identity", runnerUsername)
	}
	setpriv, err := lookupSetpriv("setpriv")
	if err != nil {
		return fmt.Errorf("runner sandbox requires setpriv: %w", err)
	}
	args := append([]string{
		"setpriv",
		"--bounding-set=-all",
		"--inh-caps=-all",
		"--ambient-caps=-all",
		"--no-new-privs",
		"--",
	}, cmd.Args...)
	cmd.Path = setpriv
	cmd.Args = args
	return nil
}
