//go:build linux

package executor

import (
	"errors"
	"os/exec"
	"os/user"
	"slices"
	"testing"
)

func TestSandbox_AppliesRunnerUidGid(t *testing.T) {
	// Given
	sandbox := &Sandbox{uid: 10002, gid: 10002, enabled: true}
	cmd := exec.Command("bash", "/script.sh")

	// When
	sandbox.applyCredential(cmd)

	// Then
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("credential = nil, want runner credential")
	}
	credential := cmd.SysProcAttr.Credential
	if credential.Uid != 10002 || credential.Gid != 10002 {
		t.Fatalf("credential = %#v, want UID/GID 10002", credential)
	}
	if !slices.Equal(credential.Groups, []uint32{10002}) {
		t.Fatalf("groups = %v, want [10002]", credential.Groups)
	}
}

func TestSandbox_FailsClosed_WhenRunnerMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	lookupRunnerUser = func(string) (*user.User, error) {
		return nil, errors.New("runner user missing")
	}
	t.Cleanup(func() { lookupRunnerUser = user.Lookup })

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without a runner identity")
	}
}

func TestSandbox_FailsClosed_WhenServiceBoundaryMissing(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "")
	lookupRunnerUser = func(string) (*user.User, error) {
		return &user.User{Uid: "10002", Gid: "10002"}, nil
	}
	t.Cleanup(func() { lookupRunnerUser = user.Lookup })

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without service boundary")
	}
}

func TestSandbox_FailsClosed_WhenCapabilityDropFails(t *testing.T) {
	// Given
	lookupSetpriv = func(string) (string, error) {
		return "", errors.New("setpriv missing")
	}
	t.Cleanup(func() { lookupSetpriv = exec.LookPath })
	sandbox := &Sandbox{enabled: true}
	cmd := exec.Command("bash", "/script.sh")

	// When
	err := sandbox.clearCapabilities(cmd)

	// Then
	if err == nil {
		t.Fatal("clear capabilities succeeded without setpriv")
	}
}

func TestClearCapabilitiesWrapsStepWithSetpriv(t *testing.T) {
	cmd := exec.Command("bash", "/script.sh")
	sandbox := &Sandbox{enabled: true}
	if err := sandbox.clearCapabilities(cmd); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"setpriv", "--bounding-set=-all", "--inh-caps=-all",
		"--ambient-caps=-all", "--no-new-privs", "--",
		"bash", "/script.sh",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
}
