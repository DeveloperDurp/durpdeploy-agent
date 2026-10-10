package executor

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainer_attempts_preserve_isolation_without_resource_quotas(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, scenario := range []string{"success", "retries"} {
			t.Run(string(runtime)+"/"+scenario, func(t *testing.T) {
				// Given
				runner, directory := containerFixture(t, runtime)
				attempts := 1
				if scenario == "retries" {
					attempts = 3
					if err := os.WriteFile(filepath.Join(directory, "run-fail"), nil, 0600); err != nil {
						t.Fatal(err)
					}
				}
				job := NewJob(JobConfig{
					ScriptBody: "exit 0", ExecutionMode: agentproto.ExecutionContainer,
					ContainerImage: "test/image", MaxRetries: 2,
				})
				// When
				err := NewExecutorWithContainers(runner).Execute(t.Context(), job, Callbacks{})
				// Then
				if (err != nil) != (scenario == "retries") {
					t.Fatalf("execution result: %v", err)
				}
				raw, err := os.ReadFile(filepath.Join(directory, "argv-history"))
				if err != nil {
					t.Fatal(err)
				}
				runs := strings.Split(strings.TrimSuffix(string(raw), "\x00\x00"), "\x00\x00")
				if len(runs) != attempts {
					t.Fatalf("got %d attempts, want %d", len(runs), attempts)
				}
				for attempt, run := range runs {
					args := strings.Split(run, "\x00")
					for _, arg := range args {
						if strings.HasPrefix(arg, "--memory") || strings.HasPrefix(arg, "--cpu") {
							t.Errorf("attempt %d requested resource control %q", attempt+1, arg)
						}
					}
					profile := "builtin"
					if runtime == agentproto.RuntimePodman {
						profile = podmanSeccompProfile
					}
					for _, required := range []string{
						"--network=none", "--read-only", "--cap-drop=ALL",
						"--security-opt=no-new-privileges", "--user=65534:65534",
						"--tmpfs=/tmp:rw,nosuid,nodev,size=64m,mode=1777",
						"--pids-limit=128", "--log-driver=none",
						"--security-opt=seccomp=" + profile, "sha256:fixed",
					} {
						if !slices.Contains(args, required) {
							t.Errorf("attempt %d omitted isolation option %q", attempt+1, required)
						}
					}
				}
				actions, err := os.ReadFile(filepath.Join(directory, "actions"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Count(string(actions), "remove\n") != attempts {
					t.Fatalf("attempt cleanup: %s", actions)
				}
			})
		}
	}
}
