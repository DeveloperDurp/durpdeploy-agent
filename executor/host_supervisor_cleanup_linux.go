//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func waitHostChildren(ctx context.Context, root int) (int, error) {
	status, stopping := 255, false
	var deadline time.Time
	for {
		if stopping {
			if time.Now().After(deadline) {
				return 255, ErrHostProcessCleanup
			}
			if err := killHostChildren(); err != nil {
				return 255, err
			}
		}
		for {
			var state syscall.WaitStatus
			pid, err := syscall.Wait4(-1, &state, syscall.WNOHANG, nil)
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			if errors.Is(err, syscall.ECHILD) {
				return status, nil
			}
			if err != nil {
				return 255, fmt.Errorf("reap host children: %w", err)
			}
			if pid == 0 {
				break
			}
			if pid == root {
				status = state.ExitStatus()
				if state.Signaled() {
					status = 128 + int(state.Signal())
				}
				if !stopping {
					stopping, deadline = true, time.Now().
						Add(hostCleanupTimeout)
				}
			}
		}
		if !stopping && ctx.Err() != nil {
			stopping, deadline = true, time.Now().Add(hostCleanupTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func killHostChildren() error {
	tasks, err := filepath.Glob("/proc/self/task/*/children")
	if err != nil || len(tasks) == 0 {
		return errors.Join(ErrHostProcessCleanup, err)
	}
	for _, task := range tasks {
		data, err := os.ReadFile(task)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read host children: %w", err)
		}
		for _, child := range strings.Fields(string(data)) {
			pid, err := strconv.Atoi(child)
			if err != nil || pid <= 0 {
				return errors.Join(ErrHostProcessCleanup, err)
			}
			// Children cannot reuse their PIDs until this worker reaps them.
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil &&
				!errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("stop host child: %w", err)
			}
		}
	}
	return nil
}
