package agentclient

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestClientContainerPolicyRejectsBeforeOptIn(t *testing.T) {
	identity := testIdentity(t)
	client := testClient(t, "https://127.0.0.1:1", identity.Fingerprint.String())
	t.Cleanup(client.Close)
	protocol := client.protocol
	policy := client.ResourcePolicy()
	if err := client.EnableContainersWithPolicy(t.Context(), executor.ContainerConfig{}, resourcepolicy.Container{}); err == nil || errors.Is(err, executor.ErrContainerUnavailable) {
		t.Fatalf("policy validation did not precede runtime access: %v", err)
	}
	if client.ContainerExecutor() != nil || client.protocol != protocol || client.ResourcePolicy() != policy {
		t.Fatal("invalid policy enabled container execution")
	}
}

func TestClientContainerOverrideValidatesCapturedCapacities(t *testing.T) {
	identity := testIdentity(t)
	legacy := testClient(t, "https://127.0.0.1:1", identity.Fingerprint.String())
	for _, group := range []string{"staging", "artifact"} {
		limits := resourcepolicy.Defaults()
		payloadBytes := int64(^uint(0)>>1) - int64(os.Getpagesize()) - limits.Container.HelperMemoryOverheadBytes - 1
		if group == "staging" {
			limits.Staging = resourcepolicy.Staging{PayloadBytes: payloadBytes, Entries: 1, ArchiveBytes: payloadBytes + 1024}
		} else {
			limits.Artifact.ExtractedBytes, limits.Artifact.StagingNodes, limits.Artifact.Files = payloadBytes, 1, 1
		}
		client, err := NewPairedWithResourcePolicy(legacy.StateDir(), "configured", executor.StartupSettings{}, limits)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		override := limits.Container
		override.HelperMemoryOverheadBytes += 2
		err = client.EnableContainersWithPolicy(t.Context(), executor.ContainerConfig{}, override)
		if err == nil || !strings.Contains(err.Error(), "memory capacity overflows") || client.ContainerExecutor() != nil || client.ResourcePolicy() != limits {
			t.Fatalf("container override ignored captured %s capacity: %v", group, err)
		}
	}
}
