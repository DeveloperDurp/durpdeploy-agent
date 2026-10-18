//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

const service = "durpdeploy-agent.service"
const serviceState = "/var/lib/durpdeploy-agent"

func TestLiveSystemd(t *testing.T) {
	if os.Getenv("AGENT_SYSTEMD_SMOKE") != "true" {
		t.Skip("run scripts/ci/check-systemd.sh inside the disposable CI environment")
	}
	if os.Geteuid() != 0 || goruntime.GOARCH != "amd64" || os.Getenv("container") != "docker" {
		t.Fatal("requires the privileged amd64 systemd CI tools container")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := command(cleanup, "systemctl", "stop", service); err != nil {
			t.Error(err)
		}
	})
	if err := systemdSmoke(ctx); err != nil {
		t.Fatal("live systemd smoke:", err)
	}
	t.Log("actual service unit: pinned pairing/polling, non-root sandbox, kernel resource limits, restart with durable trust, and graceful stop")
}

func systemdSmoke(ctx context.Context) (err error) {
	address, err := freeAddress()
	if err != nil {
		return err
	}
	if err := os.WriteFile("/etc/durpdeploy-agent.env", []byte("DURPDEPLOY_AGENT_LISTEN_ADDR="+address+"\n"), 0600); err != nil {
		return err
	}
	if _, err := command(ctx, "systemctl", "daemon-reload"); err != nil {
		return err
	}
	if _, err := command(ctx, "systemctl", "start", service); err != nil {
		return err
	}
	code, pin, err := serviceOffer(ctx)
	if err != nil {
		return err
	}
	identityDir, err := os.MkdirTemp("", "durpdeploy-systemd-server-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(identityDir)) }()
	identity, err := agenttls.LoadOrCreate(identityDir, "https://127.0.0.1")
	if err != nil {
		return err
	}
	polls := make(chan string, 16)
	server, endpoint, err := controlPlane(identity, pin, polls)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, server.Close()) }()
	if err := pair(ctx, "https://"+address, endpoint, code, pin, identity); err != nil {
		return err
	}
	version, err := command(ctx, "/usr/local/bin/durpdeploy-agent", "version")
	if err != nil {
		return err
	}
	version = strings.TrimPrefix(version, "durpdeploy-agent ")
	if err := waitForPoll(ctx, polls, version); err != nil {
		return err
	}
	if err := serviceBoundary(ctx); err != nil {
		return err
	}
	before, err := serviceSnapshot()
	if err != nil {
		return err
	}
	if err := stopService(ctx); err != nil {
		return err
	}
	// A new listener/channel on the same pinned endpoint cannot accept a stale
	// observation from the stopped process as evidence of resumed polling.
	if err := server.Close(); err != nil {
		return err
	}
	polls = make(chan string, 16)
	next, _, err := controlPlaneAt(identity, pin, polls, strings.TrimPrefix(endpoint, "https://"))
	if err != nil {
		return err
	}
	server = next
	if _, err := command(ctx, "systemctl", "start", service); err != nil {
		return err
	}
	if err := waitForPoll(ctx, polls, version); err != nil {
		return fmt.Errorf("service did not resume pinned polling after restart: %w", err)
	}
	if err := serviceBoundary(ctx); err != nil {
		return err
	}
	if err := stopService(ctx); err != nil {
		return err
	}
	after, err := serviceSnapshot()
	if err != nil || before != after {
		return fmt.Errorf("service restart changed durable pairing or permissions: %v", err)
	}
	return nil
}

func stopService(ctx context.Context) error {
	if _, err := command(ctx, "systemctl", "stop", service); err != nil {
		return err
	}
	for property, expected := range map[string]string{"Result": "success", "ExecMainStatus": "0", "MainPID": "0", "ActiveState": "inactive"} {
		value, err := command(ctx, "systemctl", "show", service, "--property="+property, "--value")
		if err != nil || value != expected {
			return fmt.Errorf("service did not stop gracefully (%s=%s): %v", property, value, err)
		}
	}
	return nil
}

func serviceOffer(ctx context.Context) (agentproto.PairingCode, agenttls.Fingerprint, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	invocation, err := command(ctx, "systemctl", "show", service, "--property=InvocationID", "--value")
	if err != nil || len(invocation) != 32 {
		return agentproto.PairingCode{}, agenttls.Fingerprint{}, fmt.Errorf("missing service invocation identity: %v", err)
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		logs, err := command(ctx, "journalctl", "_SYSTEMD_INVOCATION_ID="+invocation, "--boot", "--output=cat", "--no-pager")
		if err != nil {
			return agentproto.PairingCode{}, agenttls.Fingerprint{}, err
		}
		if code, pin, ok := parsePairingOffer(logs); ok {
			return code, pin, nil
		}
		select {
		case <-ctx.Done():
			return agentproto.PairingCode{}, agenttls.Fingerprint{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func serviceBoundary(ctx context.Context) error {
	pid, err := command(ctx, "systemctl", "show", service, "--property=MainPID", "--value")
	if number, parseErr := strconv.Atoi(pid); err != nil || parseErr != nil || number <= 1 {
		return fmt.Errorf("missing live service process: %v", err)
	}
	identity, err := user.Lookup("durpdeploy-agent")
	if err != nil {
		return err
	}
	status, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		return err
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, _ := strings.Cut(line, ":")
		values[key] = strings.Join(strings.Fields(value), " ")
	}
	for key, expected := range map[string]string{
		"Uid":        strings.TrimSpace(strings.Repeat(identity.Uid+" ", 4)),
		"Gid":        strings.TrimSpace(strings.Repeat(identity.Gid+" ", 4)),
		"NoNewPrivs": "1", "CapInh": "0000000000000000", "CapPrm": "0000000000000000",
		"CapEff": "0000000000000000", "CapBnd": "0000000000000000", "CapAmb": "0000000000000000",
	} {
		if values[key] != expected {
			return fmt.Errorf("service sandbox mismatch: %s=%s", key, values[key])
		}
	}
	cgroup, err := command(ctx, "systemctl", "show", service, "--property=ControlGroup", "--value")
	if err != nil || !strings.HasSuffix(cgroup, "/"+service) {
		return fmt.Errorf("missing service cgroup: %v", err)
	}
	for file, expected := range map[string]string{"memory.max": "536870912", "pids.max": "128", "cpu.max": "100000 100000"} {
		value, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", cgroup, file))
		if err != nil || strings.TrimSpace(string(value)) != expected {
			return fmt.Errorf("service kernel resource limit mismatch: %s=%s: %v", file, value, err)
		}
	}
	return nil
}

func serviceSnapshot() (string, error) {
	var snapshot strings.Builder
	for _, name := range []string{".", "identity.crt", "identity.key", "state.json"} {
		path := filepath.Join(serviceState, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("invalid service state %s: %v", name, err)
		}
		stat := info.Sys().(*syscall.Stat_t)
		fmt.Fprintf(&snapshot, "%s %o %d %d\n", name, info.Mode().Perm(), stat.Uid, stat.Gid)
		if name != "." {
			data, err := os.ReadFile(path)
			if err != nil {
				return "", err
			}
			fmt.Fprintf(&snapshot, "%x\n", sha256.Sum256(data))
		}
	}
	return snapshot.String(), nil
}
