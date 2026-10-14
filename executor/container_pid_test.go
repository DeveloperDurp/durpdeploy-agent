package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainer_PID_limit_is_required_only_when_launching_steps(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		runtime   agentproto.ContainerRuntime
		info      string
		supported bool
	}{
		{"Docker default seccomp", agentproto.RuntimeDocker, `{"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=default"]}`, true},
		{"Docker missing PID support", agentproto.RuntimeDocker, `{}`, false},
		{"Docker disabled PID support", agentproto.RuntimeDocker, `{"PidsLimit":false}`, false},
		{"Podman PID delegation only", agentproto.RuntimePodman, `{"host":{"cgroupControllers":["pids"]}}`, true},
		{"Podman missing PID support", agentproto.RuntimePodman, `{}`, false},
		{"Podman absent PID delegation", agentproto.RuntimePodman, `{"host":{"cgroupControllers":["cpu","memory"]}}`, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given: the runtime permits startup regardless of capability metadata.
			runner, directory := containerFixture(t, scenario.runtime)
			if err := os.WriteFile(filepath.Join(directory, "runtime-info"), []byte(scenario.info), 0o600); err != nil {
				t.Fatal(err)
			}
			started, err := NewContainerExecutor(t.Context(), ContainerConfig{
				Runtime: scenario.runtime, SocketURL: runner.SocketURL(),
				AgentID: "launch-agent", PairingIdentity: "launch-server/certificate",
			})
			if err != nil {
				t.Fatalf("capabilities blocked startup: %v", err)
			}
			t.Cleanup(func() {
				if err := started.Close(); err != nil {
					t.Error(err)
				}
			})
			if err := os.WriteFile(filepath.Join(directory, "actions"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			job := NewJob(JobConfig{
				ScriptBody: "exit 0", ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "test/image", MaxRetries: 1,
			})
			// When
			err = NewExecutorWithContainers(started).Execute(t.Context(), job, Callbacks{})
			// Then
			if scenario.supported && err != nil {
				t.Fatalf("supported PID limit: %v", err)
			}
			if !scenario.supported && !errors.Is(err, ErrContainerUnavailable) {
				t.Fatalf("unsupported PID limit: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(actions), "run\n") != scenario.supported {
				t.Fatalf("container launch with PID support=%t: %s", scenario.supported, actions)
			}
		})
	}
}

func TestContainer_PID_limit_support_is_rechecked_for_retries(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given: PID support is withdrawn after the first attempt starts.
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(filepath.Join(directory, "runtime-info-after-run"), []byte(`{}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "run-fail"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			job := NewJob(JobConfig{
				ScriptBody: "exit 7", ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "test/image", MaxRetries: 1,
			})
			// When
			err := NewExecutorWithContainers(runner).Execute(t.Context(), job, Callbacks{})
			// Then
			if !errors.Is(err, ErrContainerUnavailable) {
				t.Fatalf("unsupported retry PID limit: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(actions), "run\n") != 1 {
				t.Fatalf("retry launched without PID support: %s", actions)
			}
		})
	}
}
