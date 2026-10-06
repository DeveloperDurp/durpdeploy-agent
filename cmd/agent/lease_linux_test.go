package main

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAgentSubprocess_singleton_state_lease(t *testing.T) {
	// Given
	fixture := newAgentSubprocessFixture(t, "exit 0")
	first := fixture.start(t)
	defer func() {
		if err := first.Process.Signal(syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		if err := first.Wait(); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-fixture.result:
	case <-time.After(5 * time.Second):
		t.Fatal("first agent did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	second := exec.CommandContext(ctx, first.Path)
	second.Env = first.Env
	// When
	output, err := second.CombinedOutput()
	// Then
	if err == nil ||
		!strings.Contains(
			string(output),
			"another agent owns this state directory",
		) {
		t.Fatalf(
			"second agent entered active pairing: err=%v output=%s",
			err,
			output,
		)
	}
}
