//go:build linux

package executor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestHostChildShutdownPreservesExitStatus(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		script string
		cancel bool
		status int
	}{
		{"success", "exit 0", false, 0},
		{"failure", "exit 7", false, 7},
		{"signal", "kill -TERM $$", false, 128 + int(syscall.SIGTERM)},
		{"cancel", "exec sleep 30", true, 128 + int(syscall.SIGKILL)},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// This process-wide reaper test must not run in parallel.
			command := exec.Command("/bin/bash", "-c", scenario.script)
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				// Wait4 owns reaping. Release only the Go process handle.
				if err := command.Process.Release(); err != nil {
					t.Error(err)
				}
			})
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if scenario.cancel {
				cancel()
			}
			status, err := waitHostChildren(ctx, command.Process.Pid)
			if err != nil {
				if killErr := command.Process.Kill(); killErr != nil &&
					!errors.Is(killErr, os.ErrProcessDone) {
					t.Error(killErr)
				}
				t.Fatal(err)
			}
			if status != scenario.status {
				t.Errorf("exit status = %d, want %d", status, scenario.status)
			}
			var state syscall.WaitStatus
			if _, err := syscall.Wait4(command.Process.Pid, &state,
				syscall.WNOHANG, nil); !errors.Is(err, syscall.ECHILD) {
				t.Fatalf("job was not reaped: %v", err)
			}
		})
	}
}
