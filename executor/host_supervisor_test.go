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
	"testing"
	"time"
)

const detachedHostWriter = `setsid bash -c 'echo $$ > "$DURPDEPLOY_STAGE_DIR/child"; exec sleep 30' >/dev/null 2>&1 &
while test ! -s "$DURPDEPLOY_STAGE_DIR/child"; do sleep 0.01; done`

func TestStagedHostCancellationReapsDetachedChildren(t *testing.T) {
	directory := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	executor := newExecutorForTest(t)
	done := make(chan error, 1)
	go func() {
		done <- executor.Execute(ctx, NewJob(JobConfig{
			StageDirectory: directory,
			ScriptBody:     detachedHostWriter + "\nsleep 30",
		}), Callbacks{})
	}()
	pid := waitHostWriter(t, directory)
	cancel()
	select {
	case err := <-done:
		if err == nil || errors.Is(err, ErrHostProcessCleanup) {
			t.Fatalf("cancelled job cleanup: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled supervisor did not finish")
	}
	if _, err := os.Stat(
		fmt.Sprintf("/proc/%d/stat", pid),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("cancelled job retained child %d: %v", pid, err)
	}
}

func TestStagedHostRetryWaitsForDetachedChildren(t *testing.T) {
	directory := t.TempDir()
	script := `if test ! -e "$DURPDEPLOY_STAGE_DIR/retried"; then
touch "$DURPDEPLOY_STAGE_DIR/retried"
` + detachedHostWriter + `
exit 7
fi
test ! -e "/proc/$(cat "$DURPDEPLOY_STAGE_DIR/child")/stat"`
	err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
		StageDirectory: directory, ScriptBody: script, MaxRetries: 1,
	}), Callbacks{})
	if err != nil {
		t.Fatalf("retry overlapped a detached writer: %v", err)
	}
}

func TestStagedHostMissingConfirmationStopsRetries(t *testing.T) {
	directory := t.TempDir()
	worker := filepath.Join(directory, "worker")
	attempts := filepath.Join(directory, "attempts")
	if err := os.WriteFile(worker, []byte(fmt.Sprintf(
		"#!/bin/sh\nprintf x >> %q\nexit 0\n", attempts)), 0700); err != nil {
		t.Fatal(err)
	}
	executor := newExecutorForTest(t)
	executor.hostSupervisor = worker
	err := executor.Execute(t.Context(), NewJob(JobConfig{
		StageDirectory: directory, ScriptBody: "true", MaxRetries: 2,
	}), Callbacks{})
	data, readErr := os.ReadFile(attempts)
	if !errors.Is(err, ErrHostProcessCleanup) ||
		!errors.Is(err, ErrContainerCleanup) || readErr != nil ||
		string(data) != "x" {
		t.Fatalf("unconfirmed job retried: err=%v attempts=%q read=%v",
			err, data, readErr)
	}
}

func waitHostWriter(t *testing.T, directory string) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		data, err := os.ReadFile(filepath.Join(directory, "child"))
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && pid > 0 {
				t.Cleanup(func() { KillProcessGroup(pid) })
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("detached writer did not start")
	return 0
}
