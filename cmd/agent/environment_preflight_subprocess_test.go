package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentSubprocess_rejectsInvalidEnvironmentBeforeScript(t *testing.T) {
	for _, scenario := range []struct{ label, name, value string }{
		{"nul-name", "BAD\x00NAME", "private-value"},
		{"equals-name", "BAD=NAME", "private-value"},
		{"space-name", "bad name", "private-value"},
		{"nul-value", "VALID", "private\x00value"},
	} {
		t.Run(scenario.label, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "executed")
			fixture := newAgentSubprocessFixture(t, `touch "$MARKER"`)
			fixture.addVariable(variablePayload{Name: "MARKER", Value: marker})
			fixture.addVariable(variablePayload{Name: scenario.name, Value: scenario.value, Secret: true})
			process := fixture.start(t)
			t.Cleanup(func() {
				if err := process.Process.Signal(syscall.SIGTERM); err != nil {
					t.Error(err)
				}
				if err := process.Wait(); err != nil {
					t.Error(err)
				}
			})
			select {
			case result := <-fixture.result:
				if result.State != agentproto.ResultFailed {
					t.Fatalf("result state=%s", result.State)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("agent did not report invalid environment failure")
			}
			select {
			case <-fixture.pollAgain:
			case <-time.After(5 * time.Second):
				t.Fatal("invalid environment prevented subsequent polling")
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid environment started script: %v", err)
			}
			for _, line := range fixture.logs() {
				if strings.Contains(line, "Execution location:") || strings.Contains(line, "private") || strings.Contains(line, "subprocess-secret") {
					t.Fatalf("invalid environment attempted execution or exposed a value: %q", line)
				}
			}
		})
	}
}
