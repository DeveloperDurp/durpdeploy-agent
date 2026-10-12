package main

import (
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentSubprocess_completesOrderedStepsAndRedactsSecrets(t *testing.T) {
	// Given
	fixture := newAgentSubprocessFixture(
		t,
		`printf 'first\n'; printf '%s\n' "$SECRET"; printf 'second\n'`,
	)
	process := fixture.start(t)

	// When
	result := <-fixture.result
	process.Process.Signal(syscall.SIGTERM)
	if err := process.Wait(); err != nil {
		t.Fatalf("wait agent: %v", err)
	}

	// Then
	if result.State != agentproto.ResultSucceeded || result.Error != "" {
		t.Fatalf("result = %#v", result)
	}
	logs := fixture.logs()
	if len(logs) == 0 || !strings.Contains(logs[0], "mode=host/shell") {
		t.Fatalf("execution location missing before output: %v", logs)
	}
	if got := strings.Join(
		logs[1:],
		"\n",
	); got != "first\n[REDACTED]\nsecond" {
		t.Fatalf("logs = %q", got)
	}
	containerLogs := fixture.stderr.String()
	for _, want := range []string{
		`INFO deployment received deployment_id=42 steps=1`,
		`INFO step started deployment_id=42 step=deploy`,
		`INFO step output deployment_id=42 step=deploy output=first`,
		`INFO step output deployment_id=42 step=deploy output=[REDACTED]`,
		`INFO step finished deployment_id=42 step=deploy status=succeeded`,
		`INFO deployment execution finished deployment_id=42 status=succeeded`,
	} {
		if !strings.Contains(containerLogs, want) {
			t.Fatalf("container logs missing %q:\n%s", want, containerLogs)
		}
	}
	if strings.Contains(containerLogs, "subprocess-secret") {
		t.Fatalf("container logs contain secret:\n%s", containerLogs)
	}
	fixture.assertNoSecretFiles(t)
}

func TestAgentSubprocess_frames_large_redacted_utf8_line_inside_protocol_limits(
	t *testing.T,
) {
	// Given
	fixture := newAgentSubprocessFixture(
		t,
		`printf '%s' "$SECRET"; for ((i=0; i<100000; i++)); do printf '界\t'; done; printf '\n'`,
	)
	process := fixture.start(t)
	// When
	var result agentproto.ResultRequest
	select {
	case result = <-fixture.result:
	case <-time.After(10 * time.Second):
		_ = process.Process.Signal(syscall.SIGTERM)
		_ = process.Wait()
		t.Fatal("large log did not complete")
	}
	if err := process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); err != nil {
		t.Fatal(err)
	}
	// Then
	if result.State != agentproto.ResultSucceeded {
		t.Fatalf("result: %+v", result)
	}
	logs := fixture.logs()
	if len(logs) == 0 || !strings.Contains(logs[0], "mode=host/shell") {
		t.Fatal("execution location missing before framed output")
	}
	if got := strings.Join(
		logs[1:],
		"",
	); got != "[REDACTED]"+strings.Repeat(
		"界\t",
		100000,
	) {
		t.Fatal("framing lost data or exposed a secret")
	}
}
