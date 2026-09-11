//go:build linux

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func newExecutorForTest(t *testing.T) *Executor {
	t.Helper()
	return &Executor{}
}

func TestExecutor_Succeeds_when_script_exits_zero(t *testing.T) {
	// Given
	var logs []string
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{Name: "ok", ScriptBody: "echo complete"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(logs, "\n"); got != "complete" {
		t.Fatalf("logs = %q, want %q", got, "complete")
	}
}

func TestExecutor_CommandDoesNotUseChroot(t *testing.T) {
	// Given
	executor := newExecutorForTest(t)
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "script.sh")

	// When
	cmd := executor.command(context.Background(), tmpDir, scriptPath)

	// Then
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Chroot != "" {
		t.Fatalf("command chroot = %q, want empty", cmd.SysProcAttr.Chroot)
	}
	if cmd.Dir != tmpDir {
		t.Fatalf("command directory = %q, want %q", cmd.Dir, tmpDir)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("command lacks its own process group")
	}
	if cmd.SysProcAttr.Credential != nil {
		t.Fatalf("command switches credentials: %+v", cmd.SysProcAttr.Credential)
	}
	wantArgs := []string{"bash", scriptPath}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("command args = %q, want %q", cmd.Args, wantArgs)
	}
}

func TestExecutor_ReturnsFailure_when_script_exits_nonzero(t *testing.T) {
	// Given
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{Name: "fail", ScriptBody: "exit 7"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{}),
	)

	// Then
	if err == nil {
		t.Fatal("execute succeeded, want failure")
	}
}

func TestExecutor_Retries_when_prior_attempt_fails(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "attempted")
	var logs []string
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{
		Name:       "retry",
		ScriptBody: "test -f " + marker + " || { touch " + marker + "; exit 1; }; echo complete",
		MaxRetries: 1,
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(
		logs,
		"\n",
	); !strings.Contains(
		got,
		"retrying (attempt 2 of 2)",
	) {
		t.Fatalf("logs = %q, want retry message", got)
	}
}

func TestExecutor_ReturnsTimeout_when_step_exceeds_deadline(t *testing.T) {
	// Given
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{
		Name:       "timeout",
		ScriptBody: "sleep 1",
		Timeout:    20 * time.Millisecond,
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{}),
	)

	// Then
	if err == nil {
		t.Fatal("execute succeeded, want timeout")
	}
}

func TestExecutor_StopsRetries_when_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	executor := newExecutorForTest(t)
	job := NewJob(
		JobConfig{Name: "cancel", ScriptBody: "exit 1", MaxRetries: 1},
	)

	// When
	err := executor.Execute(ctx, job, NewCallbacks(CallbacksConfig{
		Cancelled: func() bool { return true },
	}))

	// Then
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("execute error = %v, want ErrCancelled", err)
	}
}

func TestExecutor_RedactsSecrets_when_writing_logs(t *testing.T) {
	// Given
	secret := "executor-secret"
	var logs []string
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{
		Name:       "redaction",
		ScriptBody: "printf '%s\\n' " + secret,
		Secrets:    []string{secret},
	})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(line string) error {
				logs = append(logs, line)
				return nil
			},
		}),
	)

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := strings.Join(
		logs,
		"\n",
	); strings.Contains(got, secret) ||
		!strings.Contains(got, "[REDACTED]") {
		t.Fatalf("logs = %q, want redacted secret", got)
	}
}

func TestExecutor_Fails_when_log_persistence_fails(t *testing.T) {
	// Given
	persistenceErr := errors.New("persist deployment log")
	executor := newExecutorForTest(t)
	job := NewJob(JobConfig{Name: "persist", ScriptBody: "echo complete"})

	// When
	err := executor.Execute(
		context.Background(),
		job,
		NewCallbacks(CallbacksConfig{
			WriteLog: func(string) error { return persistenceErr },
		}),
	)

	// Then
	if !errors.Is(err, persistenceErr) {
		t.Fatalf("execute error = %v, want persistence error", err)
	}
}

func TestExecutor_ExecuteSteps_stops_after_first_failed_step(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "second-step-ran")
	executor := newExecutorForTest(t)

	// When
	err := executor.ExecuteSteps(context.Background(), ExecutionConfig{
		DeploymentID: 1,
		Steps: []Step{
			{Name: "fail", ScriptBody: "exit 1"},
			{
				Name:       "must not run",
				ScriptBody: "touch " + marker,
			},
		},
	})

	// Then
	if err == nil {
		t.Fatal("execute steps succeeded, want failure")
	}
	if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("second step marker error = %v, want not exist", statErr)
	}
}
