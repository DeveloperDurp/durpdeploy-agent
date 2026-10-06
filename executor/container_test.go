package executor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func containerFixture(
	t *testing.T,
	runtime agentproto.ContainerRuntime,
) (*ContainerExecutor, string) {
	t.Helper()
	directory := t.TempDir()
	socket := filepath.Join(directory, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	script := fmt.Sprintf(`#!/bin/bash
set -eu
fixture=%q
while [[ "$1" == --* ]]; do shift; done
command=$1; shift
case "$command" in
info)
 if [[ -e "$fixture/unavailable" ]]; then exit 1; fi
 if [[ -e "$fixture/no-cpu" ]]; then printf '{}'; exit; fi
 printf '%%s' '{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=builtin"],"host":{"os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true,"seccompProfilePath":"/usr/share/containers/seccomp.json"}}}' ;;
ps)
 [[ ! -e "$fixture/owned" ]] || printf '%%s\n' owned
 ;;
rm)
 printf 'remove\n' >> "$fixture/actions"
 [[ ! -e "$fixture/cleanup-fail" ]] || exit 1
 /usr/bin/rm -f "$fixture/owned" ;;
image)
 if [[ -e "$fixture/missing" && ! -e "$fixture/pulled" ]]; then exit 1; fi
 if [[ -e "$fixture/volumes" ]]; then
   printf '%%s' '[{"Id":"sha256:fixed","Config":{"Volumes":{"/unsafe":{}}}}]'
 else printf '%%s' '[{"Id":"sha256:fixed","Config":{"Volumes":null}}]'; fi ;;
pull) printf 'pull\n' >> "$fixture/actions"; printf yes > "$fixture/pulled" ;;
run)
 printf 'run\n' >> "$fixture/actions"
 printf yes > "$fixture/owned"
 printf '%%s\n' "$@" > "$fixture/argv"
 if [[ -e "$fixture/missing-interpreter" ]]; then exit 127; fi
 if [[ -e "$fixture/run-fail" ]]; then exit 7; fi
 if [[ -e "$fixture/pwsh" ]]; then
   while [[ "$1" != sha256:fixed ]]; do shift; done
   shift
   # Emulate the container's private /tmp without writing to the host /tmp.
   args=()
   for arg in "$@"; do args+=("${arg//\/tmp\/durpdeploy-step.ps1/$fixture\/durpdeploy-step.ps1}"); done
   exec "$(cat "$fixture/pwsh")" "${args[@]}"
 fi
 printf 'ready\n'
 if [[ -e "$fixture/script-127" ]]; then printf 'script-started\n'; exit 127; fi
 if [[ -e "$fixture/excess-output" ]]; then
   while :; do printf '%%4096s' x; done
 fi
 if [[ -e "$fixture/wait" ]]; then /usr/bin/sleep 30; fi
 printf '%%s\n' "${REGION-unset}" "${DEPLOY_SECRET-unset}" "${UNRELATED-unset}"
 ;;
*) exit 2 ;;
esac
`, directory)
	binary := filepath.Join(directory, string(runtime))
	if err := os.WriteFile(binary, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	runner, err := NewContainerExecutor(
		t.Context(),
		ContainerConfig{
			Runtime:   runtime,
			SocketURL: "unix://" + socket,
			AgentID:   "fixture-agent",
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
	return runner, directory
}

func TestContainer_excess_output_stops_without_retry_and_confirms_cleanup(
	t *testing.T,
) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(
				filepath.Join(directory, "excess-output"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			job := NewJob(
				JobConfig{
					ScriptBody:     "emit output",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					Timeout:        10 * time.Second,
					MaxRetries:     2,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), job, Callbacks{})
			// Then
			if !errors.Is(err, ErrStepOutputLimit) {
				t.Fatalf("output limit: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(actions), "run\n") != 1 ||
				!strings.Contains(string(actions), "remove\n") {
				t.Fatalf("retry or missing cleanup: %s", actions)
			}
			if _, err := os.Stat(
				filepath.Join(directory, "owned"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatal("attempt remains after output overflow")
			}
		})
	}
}

func TestContainer_execution_selects_variables_and_redacts_logs(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(
				filepath.Join(directory, "missing"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			var logs []string
			job := NewJob(
				JobConfig{
					Name:           "test",
					ScriptBody:     "echo selected",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					VariableNames:  []string{"REGION", "DEPLOY_SECRET"},
					Environment: map[string]string{
						"REGION":        "west",
						"DEPLOY_SECRET": "selected-private-value",
						"UNRELATED":     "other",
						"DOCKER_HOST":   "tcp://unsafe",
					},
					Secrets: []string{"selected-private-value"},
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
				strings.Contains(output, "selected-private-value") {
				t.Fatalf("logs: %s", output)
			}
			args, err := os.ReadFile(filepath.Join(directory, "argv"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(args), "selected-private-value") ||
				strings.Contains(string(args), "other") {
				t.Fatal("values exposed in arguments")
			}
			for _, required := range []string{"--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534", "--memory=256m", "--cpus=1", "--pids-limit=128", "sha256:fixed"} {
				if !strings.Contains(string(args), required) {
					t.Fatalf("missing isolation option %s", required)
				}
			}
			profile := "builtin"
			if runtime == agentproto.RuntimePodman {
				profile = podmanSeccompProfile
			}
			if !strings.Contains(
				string(args),
				"--security-opt=seccomp="+profile,
			) {
				t.Fatal("seccomp policy inherits runtime default")
			}
			if _, err := os.Stat(
				filepath.Join(directory, "owned"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatal("attempt remains")
			}
			if _, err := os.Stat(
				filepath.Join(directory, "pulled"),
			); err != nil {
				t.Fatal("missing image not pulled")
			}
		})
	}
}

func TestContainer_cleanup_failure_blocks_retry_and_recovers(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			failure := filepath.Join(directory, "cleanup-fail")
			if err := os.WriteFile(failure, nil, 0600); err != nil {
				t.Fatal(err)
			}
			job := NewJob(
				JobConfig{
					ScriptBody:     "exit 0",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					MaxRetries:     3,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), job, Callbacks{})
			// Then
			if !errors.Is(err, ErrContainerCleanup) {
				t.Fatalf("cleanup error: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(actions), "run\n") != 1 {
				t.Fatal("retried unconfirmed cleanup")
			}
			if err := runner.Ready(
				t.Context(),
			); !errors.Is(
				err,
				ErrContainerCleanup,
			) {
				t.Fatalf("ready after failure: %v", err)
			}
			if err := os.Remove(failure); err != nil {
				t.Fatal(err)
			}
			if err := runner.Ready(t.Context()); err != nil {
				t.Fatalf("recovery: %v", err)
			}
		})
	}
}

func TestContainer_rejects_image_volumes_and_missing_interpreters(
	t *testing.T,
) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, scenario := range []string{"volumes", "missing-interpreter"} {
			t.Run(string(runtime)+"/"+scenario, func(t *testing.T) {
				// Given
				runner, directory := containerFixture(t, runtime)
				if err := os.WriteFile(
					filepath.Join(directory, scenario),
					nil,
					0600,
				); err != nil {
					t.Fatal(err)
				}
				// When
				err := NewExecutorWithContainers(
					runner,
				).Execute(t.Context(), NewJob(JobConfig{ScriptBody: "exit 0", ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "test/image", MaxRetries: 2}), Callbacks{})
				// Then
				if scenario == "volumes" &&
					!errors.Is(err, ErrInvalidStepExecution) {
					t.Fatalf("volume error: %v", err)
				}
				if scenario == "missing-interpreter" &&
					(err == nil || !strings.Contains(err.Error(), "missing in the image")) {
					t.Fatalf("interpreter error: %v", err)
				}
			})
		}
	}
}

func TestContainer_script_exit127_preserves_configured_retries(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(
				filepath.Join(directory, "script-127"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			job := NewJob(
				JobConfig{
					ScriptBody:     "exit 127",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					MaxRetries:     2,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), job, Callbacks{})
			// Then
			if err == nil || errors.Is(err, ErrInterpreterUnavailable) {
				t.Fatalf("script classified as missing interpreter: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(actions), "run\n") != 3 ||
				strings.Count(string(actions), "remove\n") != 3 {
				t.Fatalf("retry/cleanup count: %s", actions)
			}
		})
	}
}

func TestContainer_timeout_removes_the_attempt(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(
				filepath.Join(directory, "wait"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			job := NewJob(
				JobConfig{
					ScriptBody:     "sleep 30",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					Timeout:        100 * time.Millisecond,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(context.Background(), job, Callbacks{})
			// Then
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("timeout: %v", err)
			}
			if _, err := os.Stat(
				filepath.Join(directory, "owned"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatal("attempt remains after timeout")
			}
		})
	}
}

func TestContainer_cancellation_confirms_removal(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(
				filepath.Join(directory, "wait"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			job := NewJob(
				JobConfig{
					ScriptBody:     "sleep 30",
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/image",
					MaxRetries:     2,
				},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(ctx, job, NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
				if line == "ready" {
					cancel()
				}
				return nil
			}}))
			// Then
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled: %v", err)
			}
			if _, err := os.Stat(
				filepath.Join(directory, "owned"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatal("attempt remains after cancellation")
			}
		})
	}
}

func TestContainer_preflight_fails_closed_and_recovers(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			flag := filepath.Join(directory, "unavailable")
			if err := os.WriteFile(flag, nil, 0600); err != nil {
				t.Fatal(err)
			}
			// When
			err := runner.Ready(t.Context())
			// Then
			if !errors.Is(err, ErrContainerUnavailable) {
				t.Fatalf("offline: %v", err)
			}
			if err := os.Remove(flag); err != nil {
				t.Fatal(err)
			}
			if err := runner.Ready(t.Context()); err != nil {
				t.Fatalf("recovered: %v", err)
			}
			if err := os.WriteFile(
				filepath.Join(directory, "no-cpu"),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			if err := runner.Ready(
				t.Context(),
			); !errors.Is(
				err,
				ErrContainerUnavailable,
			) {
				t.Fatalf("missing resource control: %v", err)
			}
		})
	}
}

func TestContainer_preflight_rejects_unconfined_or_custom_seccomp_profiles(
	t *testing.T,
) {
	for _, profile := range []string{"builtin", "unconfined", "/custom/permissive.json", ""} {
		t.Run(profile, func(t *testing.T) {
			// Given
			runner := &ContainerExecutor{runtime: agentproto.RuntimeDocker}
			raw := fmt.Sprintf(
				`{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=%s"]}`,
				profile,
			)
			// When
			err := runner.validateRuntimeInfo([]byte(raw))
			// Then
			if (err == nil) != (profile == "builtin") {
				t.Fatalf("Docker seccomp %q: %v", profile, err)
			}
		})
	}
	for _, profile := range []string{podmanSeccompProfile, "unconfined", "/custom/permissive.json", ""} {
		t.Run(profile, func(t *testing.T) {
			// Given
			runner := &ContainerExecutor{runtime: agentproto.RuntimePodman}
			raw := fmt.Sprintf(
				`{"host":{"os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true,"seccompProfilePath":%q}}}`,
				profile,
			)
			// When
			err := runner.validateRuntimeInfo([]byte(raw))
			// Then
			if (err == nil) != (profile == podmanSeccompProfile) {
				t.Fatalf("Podman seccomp %q: %v", profile, err)
			}
		})
	}
}

func TestContainer_reconciliation_uses_the_agent_identity_namespace(
	t *testing.T,
) {
	// Given
	runner, directory := containerFixture(t, agentproto.RuntimePodman)
	if err := os.WriteFile(
		filepath.Join(directory, "owned"),
		nil,
		0600,
	); err != nil {
		t.Fatal(err)
	}
	// When
	err := runner.Ready(t.Context())
	// Then
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(
		filepath.Join(directory, "owned"),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatal("orphan remains")
	}
	if !strings.HasPrefix(runner.namespace, "agent-") ||
		len(runner.namespace) != 70 {
		t.Fatal("namespace not bound to agent identity")
	}
}
