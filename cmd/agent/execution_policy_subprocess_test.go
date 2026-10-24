package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
)

func TestAgentSubprocess_rejectsExecutionLimitsBeforeStart(t *testing.T) {
	for name, invalid := range map[string]runner.Step{
		"retry maximum":   {MaxRetries: 11},
		"timeout maximum": {TimeoutSeconds: 86401},
	} {
		t.Run(name, func(t *testing.T) { testSubprocessExecutionLimit(t, invalid) })
	}
}

func testSubprocessExecutionLimit(t *testing.T, invalid runner.Step) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "must-not-run")
	fixture := newAgentSubprocessFixture(t, `touch "$MARKER"`)
	fixture.addVariable(variablePayload{Name: "MARKER", Value: marker})
	invalid.Name, invalid.ScriptBody, invalid.SortOrder = "invalid later step", "exit 0", 2
	fixture.payload.Release.Steps = append(fixture.payload.Release.Steps, invalid)
	var starts atomic.Int32
	fixture.startAcknowledged = func() { starts.Add(1) }
	process := fixture.start(t)
	stop := sync.OnceFunc(func() {
		if err := process.Process.Signal(syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		if err := process.Wait(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	select {
	case <-fixture.pollAgain:
	case <-time.After(5 * time.Second):
		t.Fatal("rejected payload prevented subsequent polling")
	}
	stop()
	if starts.Load() != 0 || len(fixture.logs()) != 0 {
		t.Fatal("invalid snapshot started or logged execution")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("partial execution: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, claimFileName)); !os.IsNotExist(err) {
		t.Fatalf("claim marker retained: %v", err)
	}
	if !strings.Contains(fixture.stderr.String(), "invalid step execution limits") {
		t.Fatal("execution-policy diagnostic missing")
	}
}
