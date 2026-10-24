package executor

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func policyContainerFixture(t *testing.T, runtime agentproto.ContainerRuntime, policy resourcepolicy.Container) (*ContainerExecutor, string) {
	t.Helper()
	legacy, directory := stagingContainerFixture(t, runtime)
	runner, err := NewContainerExecutorWithPolicy(t.Context(), ContainerConfig{
		Runtime: runtime, SocketURL: legacy.SocketURL(), AgentID: "fixture-agent",
		PairingIdentity: "fixture-server/fixture-certificate",
	}, policy)
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

func containerPolicies() []resourcepolicy.Container {
	return []resourcepolicy.Container{
		{PIDs: 8, TmpfsBytes: 1 << 20, HelperPIDs: 8, HelperCPUs: 0.5, HelperMemoryOverheadBytes: 32 << 20},
		{PIDs: 160, TmpfsBytes: 128 << 20, HelperPIDs: 48, HelperCPUs: 2.5, HelperMemoryOverheadBytes: 256 << 20},
	}
}

func TestContainerPolicyCapturedAcrossAttempts(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, policy := range containerPolicies() {
			t.Run(fmt.Sprintf("%s/%d", runtime, policy.PIDs), func(t *testing.T) {
				testContainerPolicyAttempts(t, runtime, policy)
			})
		}
	}
}

func testContainerPolicyAttempts(t *testing.T, runtime agentproto.ContainerRuntime, policy resourcepolicy.Container) {
	t.Helper()
	runner, directory := policyContainerFixture(t, runtime, policy)
	want := policy
	policy.PIDs = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS", "1")
	if err := os.WriteFile(filepath.Join(directory, "run-fail"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(JobConfig{
		ScriptBody: "exit 7", ExecutionMode: agentproto.ExecutionContainer,
		ContainerImage: "test/image", MaxRetries: 2,
	}), Callbacks{})
	if err == nil {
		t.Fatal("accepted failing workload")
	}
	raw, err := os.ReadFile(filepath.Join(directory, "argv-history"))
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(strings.TrimSuffix(string(raw), "\x00\x00"), "\x00\x00")
	if len(runs) != 3 {
		t.Fatalf("attempt count: %d", len(runs))
	}
	for _, run := range runs {
		assertContainerPolicyAttempt(t, strings.Split(run, "\x00"), want)
	}
	actions, err := os.ReadFile(filepath.Join(directory, "actions"))
	if err != nil || strings.Count(string(actions), "remove\n") != 3 {
		t.Fatalf("cleanup: %s %v", actions, err)
	}
}

func assertContainerPolicyAttempt(t *testing.T, args []string, policy resourcepolicy.Container) {
	t.Helper()
	for _, expected := range []string{fmt.Sprintf("--pids-limit=%d", policy.PIDs), fmt.Sprintf("--tmpfs=/tmp:rw,nosuid,nodev,size=%d,mode=1777", policy.TmpfsBytes), "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534"} {
		if !slices.Contains(args, expected) {
			t.Errorf("missing %s in %v", expected, args)
		}
	}
	for _, arg := range args {
		if strings.HasPrefix(arg, "--memory") || strings.HasPrefix(arg, "--cpu") {
			t.Errorf("unexpected workload quota: %s", arg)
		}
	}
}

func TestContainerPolicyHelpersAndHandoff(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, policy := range containerPolicies() {
			t.Run(fmt.Sprintf("%s/%d", runtime, policy.HelperPIDs), func(t *testing.T) {
				testContainerPolicyHelpers(t, runtime, policy)
			})
		}
	}
}

func testContainerPolicyHelpers(t *testing.T, runtime agentproto.ContainerRuntime, policy resourcepolicy.Container) {
	t.Helper()
	runner, directory := policyContainerFixture(t, runtime, policy)
	for _, readOnly := range []bool{false, true} {
		stage, err := runner.newStage(t.Context(), bytes.NewReader(stageTestArchive(t).Bytes()), readOnly)
		if err != nil {
			t.Fatal(err)
		}
		if err := stage.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(directory, "argv-history"))
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(strings.TrimSuffix(string(raw), "\x00\x00"), "\x00\x00")
	if len(runs) != 3 {
		t.Fatalf("helper count: %d", len(runs))
	}
	capacity := int64(512<<20) + 20000*int64(os.Getpagesize())
	artifactCapacity := int64(512<<20) + 19999*int64(os.Getpagesize())
	capacities := []int64{capacity, artifactCapacity, artifactCapacity}
	for index, run := range runs {
		args := strings.Split(run, "\x00")
		for _, expected := range []string{fmt.Sprintf("--pids-limit=%d", policy.HelperPIDs), fmt.Sprintf("--cpus=%g", policy.HelperCPUs), fmt.Sprintf("--memory=%d", capacities[index]+policy.HelperMemoryOverheadBytes), "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534"} {
			if !slices.Contains(args, expected) {
				t.Errorf("missing %s in %v", expected, args)
			}
		}
	}
	if err := runner.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestContainerPolicyRejectsBeforeRuntimeEffects(t *testing.T) {
	for _, policy := range []resourcepolicy.Container{{}, {PIDs: -1}, {PIDs: math.MaxInt64}, {PIDs: 1, TmpfsBytes: 1, HelperPIDs: 1, HelperCPUs: math.NaN(), HelperMemoryOverheadBytes: 1}} {
		if _, err := NewContainerExecutorWithPolicy(t.Context(), ContainerConfig{}, policy); err == nil || !strings.Contains(err.Error(), "range") {
			t.Fatalf("invalid policy reached runtime validation: %v", err)
		}
	}
}
