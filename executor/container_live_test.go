//go:build containertest

package executor

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func liveContainerRunner(t *testing.T) *ContainerExecutor {
	t.Helper()
	runtime, err := agentproto.ParseContainerRuntime(
		os.Getenv("AGENT_TEST_RUNTIME"),
	)
	if err != nil {
		t.Fatal("AGENT_TEST_RUNTIME must select docker or podman")
	}
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	runner, err := NewContainerExecutor(
		ctx,
		ContainerConfig{
			Runtime:   runtime,
			SocketURL: os.Getenv("AGENT_TEST_SOCKET"),
			AgentID:   agentproto.AgentID(t.Name()),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runner.Close(); err != nil {
			t.Error(err)
		}
	})
	return runner
}

func TestContainerLive_interpreters_receive_selected_variables(t *testing.T) {
	// Given
	runner := liveContainerRunner(t)
	tests := []struct {
		interpreter   Interpreter
		image, script string
	}{
		{
			InterpreterBash,
			"docker.io/library/bash:5.2",
			`printf '%s\n' "$REGION" "$DEPLOY_SECRET" "${UNRELATED-unset}"; test "$(id -u)" = 65534; test ! -w /usr; test ! -e /var/run/docker.sock; test "$(cat /sys/fs/cgroup/memory.max)" = 268435456; test "$(cat /sys/fs/cgroup/pids.max)" = 128; test "$(cat /sys/fs/cgroup/cpu.max)" = '100000 100000'; grep -q '^CapEff:.*0000000000000000' /proc/self/status; grep -q '^NoNewPrivs:.*1' /proc/self/status; grep -q '^Seccomp:.*2' /proc/self/status`,
		},
		{
			InterpreterPython3,
			"docker.io/library/python:3.12-alpine",
			`import os; print(os.environ['REGION']); print(os.environ['DEPLOY_SECRET']); print(os.environ.get('UNRELATED', 'unset')); assert os.getuid() == 65534`,
		},
		{
			InterpreterPwsh,
			"mcr.microsoft.com/powershell:latest",
			`$env:REGION; $env:DEPLOY_SECRET; if ($env:UNRELATED) { throw 'unrelated variable exposed' } else { 'unset' }; if ($env:HOME -ne '/tmp') { throw 'invalid home' }`,
		},
	}
	for _, test := range tests {
		t.Run(string(test.interpreter), func(t *testing.T) {
			var logs []string
			job := NewJob(
				JobConfig{
					Name:           test.image,
					ScriptBody:     test.script,
					Interpreter:    test.interpreter,
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: test.image,
					VariableNames:  []string{"REGION", "DEPLOY_SECRET"},
					Environment: map[string]string{
						"REGION":        "west",
						"DEPLOY_SECRET": "live-private-value",
						"UNRELATED":     "must-not-pass",
					},
					Secrets: []string{"live-private-value"},
					Timeout: 5 * time.Minute,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), job, NewCallbacks(CallbacksConfig{WriteLog: func(line string) error { logs = append(logs, line); return nil }}))
			// Then
			if err != nil {
				t.Fatal(err)
			}
			output := strings.Join(logs, "\n")
			if !strings.Contains(output, "west") ||
				!strings.Contains(output, "[REDACTED]") ||
				!strings.Contains(output, "unset") ||
				strings.Contains(output, "live-private-value") ||
				strings.Contains(output, "must-not-pass") {
				t.Fatalf("output: %s", output)
			}
			ids, err := runner.ownedContainers(t.Context())
			if err != nil || len(ids) != 0 {
				t.Fatalf("containers remain: %v, %v", ids, err)
			}
		})
	}
}

func TestContainerLive_timeout_and_cancellation_remove_attempts(t *testing.T) {
	// Given
	runner := liveContainerRunner(t)
	for _, scenario := range []string{"timeout", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			timeout := 3 * time.Second
			if scenario == "cancel" {
				timeout = time.Minute
			}
			job := NewJob(
				JobConfig{
					ScriptBody:     "echo ready; sleep 30",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "docker.io/library/bash:5.2",
					Timeout:        timeout,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(ctx, job, NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
				if scenario == "cancel" && line == "ready" {
					cancel()
				}
				return nil
			}}))
			// Then
			want := context.DeadlineExceeded
			if scenario == "cancel" {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("interruption: %v", err)
			}
			ids, err := runner.ownedContainers(t.Context())
			if err != nil || len(ids) != 0 {
				t.Fatalf("containers remain: %v, %v", ids, err)
			}
		})
	}
}

func TestContainerLive_excess_output_removes_attempt(t *testing.T) {
	// Given
	runner := liveContainerRunner(t)
	job := NewJob(JobConfig{
		ScriptBody:     "while :; do printf '%4096s' x; done",
		ExecutionMode:  agentproto.ExecutionContainer,
		ContainerImage: "docker.io/library/bash:5.2",
		Timeout:        time.Minute,
		MaxRetries:     2,
	})
	// When
	err := NewExecutorWithContainers(
		runner,
	).Execute(t.Context(), job, Callbacks{})
	// Then
	if !errors.Is(err, ErrStepOutputLimit) {
		t.Fatalf("output limit: %v", err)
	}
	ids, err := runner.ownedContainers(t.Context())
	if err != nil || len(ids) != 0 {
		t.Fatalf("containers remain: %v, %v", ids, err)
	}
}

func TestContainerLive_script_exit127_retries_and_missing_interpreter_reports_failure(
	t *testing.T,
) {
	// Given
	runner := liveContainerRunner(t)
	for _, interpreter := range []Interpreter{InterpreterBash, InterpreterPython3} {
		t.Run(string(interpreter), func(t *testing.T) {
			var logs []string
			job := NewJob(
				JobConfig{
					Interpreter:    interpreter,
					ScriptBody:     "echo script-started; exit 127",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "docker.io/library/bash:5.2",
					Timeout:        time.Minute,
					MaxRetries:     2,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), job, NewCallbacks(CallbacksConfig{
				WriteLog: func(line string) error { logs = append(logs, line); return nil },
			}))
			// Then
			if err == nil || errors.Is(err, ErrInterpreterUnavailable) ||
				!strings.Contains(err.Error(), "missing in the image") {
				t.Fatalf("failure classification: %v", err)
			}
			if interpreter == InterpreterBash &&
				strings.Count(strings.Join(logs, "\n"), "script-started") != 3 {
				t.Fatalf("script exit skipped retries: %v", logs)
			}
			ids, err := runner.ownedContainers(t.Context())
			if err != nil || len(ids) != 0 {
				t.Fatalf("containers remain: %v, %v", ids, err)
			}
		})
	}
}
