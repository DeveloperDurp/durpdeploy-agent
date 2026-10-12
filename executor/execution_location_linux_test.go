//go:build linux

package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHostLogsPhysicalLocationBeforeEveryAttempt(t *testing.T) {
	// Given: the agent operator identifies the physical host.
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "physical-host")
	job := NewJob(JobConfig{
		ScriptBody: "printf 'script-output\\n'; exit 4", MaxRetries: 1,
	})
	var logs []string

	// When: a failed host step retries.
	err := newExecutorForTest(t).Execute(t.Context(), job,
		NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
			logs = append(logs, line)
			return nil
		}}),
	)

	// Then: each interpreter launch has its physical location first.
	if err == nil {
		t.Fatal("failure script succeeded")
	}
	attempts := 0
	for index, line := range logs {
		if line != "script-output" {
			continue
		}
		attempts++
		if index == 0 {
			t.Fatal("script output preceded execution location")
		}
		location := logs[index-1]
		for _, field := range []string{
			`host="physical-host"`, `mode=host/shell`, `interpreter="bash"`,
			fmt.Sprintf("attempt=%d", attempts),
		} {
			if !strings.Contains(location, field) {
				t.Errorf("host execution location missing %q: %s", field, location)
			}
		}
	}
	if attempts != 2 {
		t.Fatalf("attempts=%d logs=%v", attempts, logs)
	}
}

func TestHostDoesNotExecuteWhenLocationLogFails(t *testing.T) {
	// Given: saving the first execution location fails.
	marker := filepath.Join(t.TempDir(), "effects")
	job := NewJob(JobConfig{
		ScriptBody: fmt.Sprintf("echo effect >> %q", marker), MaxRetries: 2,
	})
	failure := errors.New("location upload unavailable")

	// When: the executor tries to launch the job.
	err := newExecutorForTest(t).Execute(t.Context(), job,
		NewCallbacks(CallbacksConfig{WriteLog: func(string) error { return failure }}),
	)

	// Then: log delivery fails without running or retrying the script.
	if !errors.Is(err, ErrLogDelivery) || !errors.Is(err, failure) {
		t.Fatalf("log failure=%v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unlogged script executed: %v", err)
	}
}
