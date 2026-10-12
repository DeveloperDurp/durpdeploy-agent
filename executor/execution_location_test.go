package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainerLogsPhysicalHostAndEveryAttempt(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{
		agentproto.RuntimeDocker, agentproto.RuntimePodman,
	} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given: retries execute on a runtime with a physical host name.
			t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "")
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(filepath.Join(directory, "script-127"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			job := NewJob(JobConfig{
				Name: "Deploy", ScriptBody: "exit 127", MaxRetries: 1,
				ExecutionMode:  agentproto.ExecutionContainer,
				ContainerImage: "test/image",
			})
			var logs []string

			// When: the first attempt fails and the executor retries.
			err := NewExecutorWithContainers(runner).Execute(t.Context(), job,
				NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
					logs = append(logs, line)
					return nil
				}}),
			)

			// Then: each attempt's location precedes its output.
			if err == nil {
				t.Fatal("failure script succeeded")
			}
			var locations []string
			lastLocation := false
			for _, line := range logs {
				if strings.HasPrefix(line, "Execution location:") {
					locations = append(locations, line)
					lastLocation = true
				}
				if line == "ready" {
					if !lastLocation {
						t.Fatal("script output preceded its execution location")
					}
					lastLocation = false
				}
			}
			if len(locations) != 2 || locations[0] == locations[1] {
				t.Fatalf("retry locations=%v", locations)
			}
			host := "physical-docker"
			if runtime == agentproto.RuntimePodman {
				host = "physical-podman"
			}
			for _, line := range locations {
				for _, field := range []string{
					`host="` + host + `"`, `mode=container`,
					`runtime="` + string(runtime) + `"`,
					`image="sha256:fixed"`, `interpreter="bash"`,
				} {
					if !strings.Contains(line, field) {
						t.Errorf("execution location missing %q: %s", field, line)
					}
				}
			}
		})
	}
}

func TestExecutionHostnameRequiresPhysicalNameInContainer(t *testing.T) {
	// Given: the supervisor is containerized and cannot inspect a runtime.
	t.Setenv("container", "podman")
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "")

	// When: its execution hostname is requested.
	host := executionHostname("")

	// Then: it does not present the supervisor hostname as a physical host.
	if host != "unknown" {
		t.Fatalf("physical host=%q", host)
	}
}

func TestExecutionHostnameUsesOperatorName(t *testing.T) {
	// Given: the operator supplies the physical name, distinct from runtime data.
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "operator-physical-host")

	// When: an execution location is resolved.
	host := executionHostname("runtime-reported-host")

	// Then: the configured physical name wins.
	if host != "operator-physical-host" {
		t.Fatalf("physical host=%q", host)
	}
}
