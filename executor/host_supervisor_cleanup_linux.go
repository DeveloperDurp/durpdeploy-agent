//go:build linux

package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
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
	processes, err := os.ReadDir("/proc")
	if err != nil {
		return fmt.Errorf("list host processes: %w", err)
	}
	parent := strconv.Itoa(os.Getpid())
	for _, process := range processes {
		pid, err := strconv.Atoi(process.Name())
		if err != nil || pid <= 0 || !process.IsDir() {
			continue
		}
		data, err := os.ReadFile("/proc/" + process.Name() + "/stat")
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
			continue
		}
		if err != nil {
			return fmt.Errorf("read host process: %w", err)
		}
		// The command name can contain spaces or parentheses. PPID follows
		// the final closing parenthesis and the process state field.
		end := strings.LastIndexByte(string(data), ')')
		if end < 0 {
			return ErrHostProcessCleanup
		}
		fields := strings.Fields(string(data[end+1:]))
		if len(fields) < 2 {
			return ErrHostProcessCleanup
		}
		if fields[1] != parent {
			continue
		}
		// Children cannot reuse their PIDs until this worker reaps them.
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil &&
			!errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("stop host child: %w", err)
		}
	}
	return nil
}
