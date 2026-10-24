package main

import (
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentSubprocess_usesCapturedYAMLExecutionSettings(t *testing.T) {
	fixture := newAgentSubprocessFixture(t, "echo CUSTOM_123")
	file := startupConfigFile(t, "hostname: yaml-physical-host\nextra_scrub_patterns: CUSTOM_[0-9]+\n")
	process := fixture.start(t, "DURPDEPLOY_AGENT_CONFIG_FILE="+file)
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
		if result.State != agentproto.ResultSucceeded {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("configured deployment did not finish")
	}
	logs := strings.Join(fixture.logs(), "\n")
	if !strings.Contains(logs, `host="yaml-physical-host"`) || strings.Contains(logs, "CUSTOM_123") || !strings.Contains(logs, "[REDACTED]") {
		t.Fatalf("configured execution logs=%s", logs)
	}
}
