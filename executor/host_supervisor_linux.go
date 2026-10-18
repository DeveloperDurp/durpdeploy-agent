//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
)

// HostSupervisorMain handles the internal worker invocation. Call it before
// application startup, and exit with its status when handled is true.
func HostSupervisorMain() (status int, handled bool) {
	if len(os.Args) < 2 || os.Args[1] != hostSupervisorArgument {
		return 0, false
	}
	if len(os.Args) != 4 || !filepath.IsAbs(os.Args[2]) ||
		!filepath.IsAbs(os.Args[3]) {
		return 255, true
	}
	report := os.NewFile(3, "host-supervisor-report")
	control := os.NewFile(4, "host-supervisor-control")
	defer func() {
		if err := report.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close host supervisor report:", err)
		}
	}()
	defer func() {
		if err := control.Close(); err != nil {
			fmt.Fprintln(os.Stderr, "close host supervisor control:", err)
		}
	}()
	// Interpreters must not inherit the confirmation or cancellation handles.
	syscall.CloseOnExec(3)
	syscall.CloseOnExec(4)
	ctx, cancel := signal.NotifyContext(context.Background(),
		syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	go func() {
		_, err := io.Copy(io.Discard, control)
		if err != nil && !errors.Is(err, os.ErrClosed) {
			fmt.Fprintln(os.Stderr, "host supervisor control failed")
		}
		cancel()
	}()
	status, err := superviseHost(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 255, true
	}
	if _, err := io.WriteString(report, hostSupervisorStopped); err != nil {
		return 255, true
	}
	return status, true
}

func superviseHost(ctx context.Context) (int, error) {
	// This worker owns only one job. Orphaned descendants remain its children,
	// including processes that change groups, sessions, or double-fork.
	if _, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL,
		36, 1, 0, 0, 0, 0); err != 0 { // PR_SET_CHILD_SUBREAPER
		return 255, fmt.Errorf("enable child subreaper: %w", err)
	}
	if _, _, err := syscall.RawSyscall6(syscall.SYS_PRCTL,
		4, 0, 0, 0, 0, 0); err != 0 { // PR_SET_DUMPABLE
		return 255, fmt.Errorf("protect supervisor handles: %w", err)
	}
	if ctx.Err() != nil {
		return 255, nil
	}
	command := exec.Command(os.Args[2], os.Args[3])
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	if err := command.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start host interpreter:", err)
		return 255, nil
	}
	defer func() {
		if err := command.Process.Release(); err != nil {
			fmt.Fprintln(os.Stderr, "release reaped host interpreter:", err)
		}
	}()
	// Reap explicitly instead of cmd.Wait: a single owner must collect the
	// interpreter and all adopted children without competing wait calls.
	return waitHostChildren(ctx, command.Process.Pid)
}
