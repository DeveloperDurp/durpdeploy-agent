//go:build linux

package executor

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestExecutionPolicyRejectsJobsBeforeEffects(t *testing.T) {
	policy := resourcepolicy.Execution{MaxRetries: 1, MaxTimeoutSeconds: 2, DefaultTimeoutSeconds: 1}
	for name, config := range map[string]JobConfig{
		"negative timeout": {Timeout: -time.Nanosecond},
		"timeout exceeded": {Timeout: 2*time.Second + time.Nanosecond},
		"negative retries": {MaxRetries: -1},
		"retries exceeded": {MaxRetries: 2},
	} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "effects")
			config.ScriptBody = "touch '" + marker + "'"
			logs := 0
			err := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, policy, nil).
				Execute(t.Context(), NewJob(config), NewCallbacks(CallbacksConfig{WriteLog: func(string) error { logs++; return nil }}))
			if !errors.Is(err, resourcepolicy.ErrExecutionLimits) || logs != 0 {
				t.Fatalf("rejection: error=%v logs=%d", err, logs)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("script ran: %v", err)
			}
		})
	}
}

func TestExecutionPolicyCapturesRetryBudget(t *testing.T) {
	policy := resourcepolicy.Execution{MaxRetries: 12, MaxTimeoutSeconds: 2, DefaultTimeoutSeconds: 1}
	withRetries := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, policy, nil)
	policy.MaxRetries = 0
	withoutRetries := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, policy, nil)
	job := NewJob(JobConfig{ScriptBody: "exit 7", MaxRetries: 11})
	started := 0
	err := withRetries.Execute(t.Context(), job, NewCallbacks(CallbacksConfig{TrackProcessGroup: func(int) { started++ }}))
	if err == nil || errors.Is(err, resourcepolicy.ErrExecutionLimits) || started != 12 {
		t.Fatalf("raised retry budget: error=%v starts=%d", err, started)
	}
	err = withoutRetries.Execute(t.Context(), job, Callbacks{})
	if !errors.Is(err, resourcepolicy.ErrExecutionLimits) {
		t.Fatalf("separate policy accepted retries: %v", err)
	}
	err = withoutRetries.Execute(t.Context(), NewJob(JobConfig{ScriptBody: "exit 0"}), Callbacks{})
	if err != nil {
		t.Fatalf("zero retries rejected: %v", err)
	}
}

func TestExecutionPolicyUsesConfiguredDefaultTimeout(t *testing.T) {
	for _, mode := range []string{"host", "docker", "podman"} {
		t.Run(mode, func(t *testing.T) { testExecutionPolicyDefaultTimeout(t, mode) })
	}
}

func testExecutionPolicyDefaultTimeout(t *testing.T, mode string) {
	t.Helper()
	var container *ContainerExecutor
	job := JobConfig{ScriptBody: "exec sleep 30"}
	if mode != "host" {
		var dir string
		container, dir = containerFixture(t, agentproto.ContainerRuntime(mode))
		if err := os.WriteFile(filepath.Join(dir, "wait"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		job.ExecutionMode, job.ContainerImage = agentproto.ExecutionContainer, "test/image"
	}
	runner := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, resourcepolicy.Execution{
		MaxRetries: 0, MaxTimeoutSeconds: 10, DefaultTimeoutSeconds: 1,
	}, container)
	runner.deadlineGrace = func() {}
	var lines []string
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := runner.Execute(ctx, NewJob(job), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error { lines = append(lines, line); return nil }}))
	if !errors.Is(err, ErrStepTimeout) || ctx.Err() != nil {
		t.Fatalf("configured default: error=%v parent=%v", err, ctx.Err())
	}
	if mode == "host" && !strings.Contains(strings.Join(lines, "\n"), "timed out after 1s") {
		t.Fatalf("wrong timeout diagnostic: %v", lines)
	}
}

func TestExecutionPolicyPreflightsWholeSnapshot(t *testing.T) {
	for name, step := range map[string]Step{
		"retry exceeded":    {MaxRetries: 2},
		"duration exceeded": {TimeoutSeconds: 3},
		"duration overflow": {TimeoutSeconds: math.MaxInt64},
		"retry overflow":    {MaxRetries: math.MaxInt64},
	} {
		t.Run(name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "must-not-run")
			runner := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, resourcepolicy.Execution{
				MaxRetries: 1, MaxTimeoutSeconds: 2, DefaultTimeoutSeconds: 1,
			}, nil)
			err := runner.ExecuteSteps(t.Context(), ExecutionConfig{Steps: []Step{
				{ScriptBody: "touch '" + marker + "'"}, step,
			}})
			if !errors.Is(err, resourcepolicy.ErrExecutionLimits) {
				t.Fatalf("snapshot accepted: %v", err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("partial execution: %v", err)
			}
		})
	}
}

func TestExecutionPolicyInvalidConfigurationAndLargestRetry(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "invalid-policy-effects")
	invalid := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, resourcepolicy.Execution{}, nil)
	if err := invalid.Execute(t.Context(), NewJob(JobConfig{ScriptBody: "touch '" + marker + "'"}), Callbacks{}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid policy ran a script: %v", err)
	}
	policy := resourcepolicy.Defaults().Execution
	policy.MaxRetries = int64(^uint(0)>>1) - 1
	runner := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, policy, nil)
	if err := runner.Execute(t.Context(), NewJob(JobConfig{ScriptBody: "exit 0", MaxRetries: int(policy.MaxRetries)}), Callbacks{}); err != nil {
		t.Fatal(err)
	}
}

func TestExecutionPolicyRaisedTimeoutAndFractionalDuration(t *testing.T) {
	policy := resourcepolicy.Execution{MaxRetries: 0, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 600}
	runner := NewExecutorWithExecutionPolicy(StartupSettings{ExecutionBoundary: "service"}, policy, nil)
	for _, timeout := range []time.Duration{48 * time.Hour, 1500 * time.Millisecond} {
		if err := runner.Execute(t.Context(), NewJob(JobConfig{ScriptBody: "exit 0", Timeout: timeout}), Callbacks{}); err != nil {
			t.Fatalf("duration %s rejected: %v", timeout, err)
		}
	}
}
