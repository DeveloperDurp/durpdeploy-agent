package executor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainer_startup_accepts_runtime_info_without_capability_checks(t *testing.T) {
	for _, scenario := range []struct {
		name    string
		runtime agentproto.ContainerRuntime
		info    string
	}{
		{
			name:    "Docker 20.10 default seccomp",
			runtime: agentproto.RuntimeDocker,
			info:    `{"Name":"hermes-agent","ServerVersion":"20.10.24+dfsg1","OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=apparmor","name=seccomp,profile=default","name=cgroupns"]}`,
		},
		{
			name:    "Docker missing capabilities",
			runtime: agentproto.RuntimeDocker,
			info:    `{"Name":"docker-host"}`,
		},
		{
			name:    "Docker unsupported capabilities",
			runtime: agentproto.RuntimeDocker,
			info:    `{"Name":"docker-host","OSType":"windows","MemoryLimit":false,"CpuCfsQuota":false,"PidsLimit":false,"SecurityOptions":["name=seccomp,profile=unconfined"]}`,
		},
		{
			name:    "Podman missing capabilities",
			runtime: agentproto.RuntimePodman,
			info:    `{"host":{"hostname":"podman-host"}}`,
		},
		{
			name:    "Podman unsupported capabilities",
			runtime: agentproto.RuntimePodman,
			info:    `{"host":{"hostname":"podman-host","os":"windows","cgroupControllers":[],"security":{"seccompEnabled":false,"seccompProfilePath":"unconfined"}}}`,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, scenario.runtime)
			if err := os.WriteFile(filepath.Join(directory, "runtime-info"), []byte(scenario.info), 0o600); err != nil {
				t.Fatal(err)
			}
			// When
			started, err := NewContainerExecutor(t.Context(), ContainerConfig{
				Runtime: scenario.runtime, SocketURL: runner.SocketURL(),
				AgentID: "startup-agent", PairingIdentity: "startup-server/certificate",
			})
			// Then
			if err != nil {
				t.Fatalf("runtime capabilities blocked startup: %v", err)
			}
			if err := started.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestContainer_preflight_rejects_unusable_runtime_info(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, info := range []string{`{`, `{"ServerErrors":["cannot connect to Docker daemon"]}`} {
			t.Run(string(runtime)+"/"+info, func(t *testing.T) {
				// Given
				runner, directory := containerFixture(t, runtime)
				if err := os.WriteFile(filepath.Join(directory, "runtime-info"), []byte(info), 0o600); err != nil {
					t.Fatal(err)
				}
				// When
				err := runner.Ready(t.Context())
				// Then
				if !errors.Is(err, ErrContainerUnavailable) {
					t.Fatalf("unusable runtime information: %v", err)
				}
			})
		}
	}
}
