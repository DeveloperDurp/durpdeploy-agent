package agentclient

import (
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestPollPolicyCapturesContainerOverride(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) { checkPollContainerOverride(t, runtime) })
	}
}

func checkPollContainerOverride(t *testing.T, runtime agentproto.ContainerRuntime) {
	t.Helper()
	directory := t.TempDir()
	socket := filepath.Join(directory, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	const script = `#!/bin/sh
while [ "${1#--}" != "$1" ]; do shift; done
case "$1" in
info) echo '{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=builtin"],"host":{"os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true,"seccompProfilePath":"/usr/share/containers/seccomp.json"}}}' ;;
ps|volume) exit 0 ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, string(runtime)), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	limits := resourcepolicy.Defaults()
	limits.Execution.MaxRetries = 12
	expected := limits
	expected.Container.PIDs = 24
	expected.Container.HelperPIDs = 8
	expected.Container.HelperCPUs = .5
	advertisement := testPolicyAdvertisement(t, expected)
	identity := testIdentity(t)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		checkPollPolicyRequest(t, r, advertisement)
		w.Header().Set(resourcepolicy.CapabilityHeader, advertisement)
		w.WriteHeader(http.StatusNoContent)
	})
	legacy := testClient(t, server.URL, identity.Fingerprint.String())
	client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	override := expected.Container
	if err := client.EnableContainersWithPolicy(t.Context(), executor.ContainerConfig{Runtime: runtime, SocketURL: "unix://" + socket}, override); err != nil {
		t.Fatal(err)
	}
	override.PIDs++
	if client.ResourcePolicy() != expected {
		t.Fatal("effective snapshot does not match executor override")
	}
	if claim, err := client.Poll(t.Context()); err != nil || claim != nil {
		t.Fatalf("overridden poll: claim=%v err=%v", claim, err)
	}
}
