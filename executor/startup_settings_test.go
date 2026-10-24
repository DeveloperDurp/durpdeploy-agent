//go:build linux

package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestConfiguredExecutorCapturesStartupSettings(t *testing.T) {
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "invalid-ambient-boundary")
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "ambient-host")
	t.Setenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS", "AMBIENT_[0-9]+")
	settings := StartupSettings{
		ExecutionBoundary: "service", Hostname: "captured-host",
		ExtraScrubPatterns: "CUSTOM_[0-9]+",
	}
	first := NewExecutorWithSettings(settings, nil)
	settings.Hostname = "second-host"
	settings.ExtraScrubPatterns = "OTHER_[0-9]+"
	second := NewExecutorWithSettings(settings, nil)
	settings.Hostname = "mutated-after-construction"
	for _, scenario := range []struct {
		executor              *Executor
		host, masked, visible string
	}{
		{first, "captured-host", "CUSTOM_123", "OTHER_456"},
		{second, "second-host", "OTHER_456", "CUSTOM_123"},
	} {
		var lines []string
		err := scenario.executor.Execute(t.Context(), NewJob(JobConfig{
			ScriptBody: "printf '%s\\n' CUSTOM_123 OTHER_456 AMBIENT_789 registered-secret",
			Secrets:    []string{"registered-secret"},
		}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
			lines = append(lines, line)
			return nil
		}}))
		logs := strings.Join(lines, "\n")
		if err != nil || !strings.Contains(logs, `host="`+scenario.host+`"`) ||
			strings.Contains(logs, scenario.masked) || strings.Contains(logs, "registered-secret") ||
			!strings.Contains(logs, scenario.visible) || !strings.Contains(logs, "AMBIENT_789") {
			t.Fatalf("captured settings: error=%v logs=%s", err, logs)
		}
	}
}

func TestConfiguredExecutorEmptyHostnameIgnoresAmbientOverride(t *testing.T) {
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "must-not-use-ambient")
	t.Setenv("container", "podman")
	executor := NewExecutorWithSettings(StartupSettings{ExecutionBoundary: "service"}, nil)
	var lines []string
	err := executor.Execute(t.Context(), NewJob(JobConfig{ScriptBody: "echo complete"}), NewCallbacks(CallbacksConfig{
		WriteLog: func(line string) error { lines = append(lines, line); return nil },
	}))
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), `host="unknown"`) {
		t.Fatalf("empty override: error=%v logs=%v", err, lines)
	}
}

func TestConfiguredExecutorFailsBeforeScriptEffects(t *testing.T) {
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	for name, settings := range map[string]StartupSettings{
		"missing captured boundary":      {},
		"invalid captured scrub pattern": {ExecutionBoundary: "service", ExtraScrubPatterns: "[private-input"},
	} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-run")
			err := NewExecutorWithSettings(settings, nil).Execute(t.Context(), NewJob(JobConfig{
				ScriptBody: "touch '" + marker + "'",
			}), NewCallbacks(CallbacksConfig{}))
			if err == nil || strings.Contains(err.Error(), "private-input") {
				t.Fatalf("expected redacted startup rejection: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("script effects occurred: %v", err)
			}
		})
	}
}

func TestConfiguredContainerExecutionCapturesHostname(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			runtime, _ := containerFixture(t, runtime)
			executor := NewExecutorWithSettings(StartupSettings{
				ExecutionBoundary: "service", Hostname: "captured-container-host",
			}, runtime)
			t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "changed-ambient-host")
			var lines []string
			err := executor.Execute(t.Context(), NewJob(JobConfig{
				ScriptBody: "echo complete", ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "test/image",
			}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
				lines = append(lines, line)
				return nil
			}}))
			if err != nil || !strings.Contains(strings.Join(lines, "\n"), `host="captured-container-host"`) {
				t.Fatalf("container hostname: error=%v logs=%v", err, lines)
			}
		})
	}
}

func TestConfiguredAndLegacyScrubberErrorsFailClosed(t *testing.T) {
	const invalid = "[private-pattern"
	if _, err := NewScrubberWithPatterns(nil, invalid); err == nil || strings.Contains(err.Error(), "private-pattern") {
		t.Fatalf("invalid pattern error=%v", err)
	}
	t.Setenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS", invalid)
	if got := NewScrubber([]string{"private-value"}).Scrub("private-value and all output"); got != "[REDACTED]" {
		t.Fatalf("legacy invalid pattern did not fail closed: %q", got)
	}
}
