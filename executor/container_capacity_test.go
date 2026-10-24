package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func capacityPolicies() []resourcepolicy.Limits {
	var policies []resourcepolicy.Limits
	for index, container := range containerPolicies() {
		policy := resourcepolicy.Defaults()
		policy.Container = container
		policy.Staging = resourcepolicy.Staging{PayloadBytes: 1 << 20, Entries: 32, ArchiveBytes: 2 << 20}
		policy.Artifact.ExtractedBytes, policy.Artifact.StagingNodes, policy.Artifact.Files = 2<<20, 64, 32
		if index != 0 {
			policy.Staging = resourcepolicy.Staging{PayloadBytes: 768 << 20, Entries: 25000, ArchiveBytes: 800 << 20}
			policy.Artifact.ExtractedBytes, policy.Artifact.StagingNodes, policy.Artifact.Files = 640<<20, 24000, 12000
		}
		policies = append(policies, policy)
	}
	return policies
}

func capacityContainerFixture(t *testing.T, runtime agentproto.ContainerRuntime, policy resourcepolicy.Limits) (*ContainerExecutor, string) {
	t.Helper()
	legacy, directory := stagingContainerFixture(t, runtime)
	runner, err := NewContainerExecutorWithResourcePolicy(t.Context(), ContainerConfig{
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

func TestContainerCapturedStageCapacities(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, policy := range capacityPolicies() {
			t.Run(fmt.Sprintf("%s/%d", runtime, policy.Staging.PayloadBytes), func(t *testing.T) {
				checkCapturedStageCapacities(t, runtime, policy)
			})
		}
	}
}

func checkCapturedStageCapacities(t *testing.T, runtime agentproto.ContainerRuntime, policy resourcepolicy.Limits) {
	t.Helper()
	runner, directory := capacityContainerFixture(t, runtime, policy)
	want := policy
	policy.Staging.PayloadBytes, policy.Staging.Entries = 1, 1
	policy.Artifact.ExtractedBytes, policy.Artifact.StagingNodes = 1, 1
	policy.Container.HelperMemoryOverheadBytes = 1
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_STAGING_PAYLOAD_BYTES", "1")
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_ARTIFACT_EXTRACTED_BYTES", "1")
	for _, readOnly := range []bool{false, true} {
		checkCapacityStage(t, runner, directory, want, readOnly)
	}
	if err := runner.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func checkCapacityStage(t *testing.T, runner *ContainerExecutor, directory string, policy resourcepolicy.Limits, readOnly bool) {
	t.Helper()
	for _, name := range []string{"actions", "argv-history"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	create := runner.NewStage
	if readOnly {
		create = runner.NewArtifactStage
	}
	stage, err := create(t.Context(), stageTestArchive(t))
	if stage != nil {
		t.Cleanup(func() { cleanupCapacityStage(t, stage) })
	}
	if err != nil {
		t.Fatal(err)
	}
	assertCapacityArguments(t, directory, policy, readOnly)
	if err := stage.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(runner.stages) != 0 {
		t.Fatal("closed capacity stage remains registered")
	}
}

func assertCapacityArguments(t *testing.T, directory string, policy resourcepolicy.Limits, readOnly bool) {
	t.Helper()
	payloadBytes, nodes, helpers := policy.Staging.PayloadBytes, policy.Staging.Entries, 1
	if readOnly {
		payloadBytes, nodes, helpers = policy.Artifact.ExtractedBytes, policy.Artifact.StagingNodes, 2
	}
	capacity := payloadBytes + nodes*int64(os.Getpagesize())
	actions, err := os.ReadFile(filepath.Join(directory, "actions"))
	if err != nil || !strings.Contains(string(actions), fmt.Sprintf("size=%d,nr_inodes=%d,", capacity, 2*(nodes+1))) {
		t.Fatalf("wrong volume capacity: %s %v", actions, err)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "argv-history"))
	if err != nil {
		t.Fatal(err)
	}
	runs := strings.Split(strings.TrimSuffix(string(raw), "\x00\x00"), "\x00\x00")
	if len(runs) != helpers {
		t.Fatalf("helper count=%d want=%d", len(runs), helpers)
	}
	for _, run := range runs {
		assertCapacityHelper(t, strings.Split(run, "\x00"), policy.Container, capacity)
	}
}

func assertCapacityHelper(t *testing.T, args []string, policy resourcepolicy.Container, capacity int64) {
	t.Helper()
	for _, want := range []string{fmt.Sprintf("--memory=%d", capacity+policy.HelperMemoryOverheadBytes), fmt.Sprintf("--pids-limit=%d", policy.HelperPIDs), fmt.Sprintf("--cpus=%g", policy.HelperCPUs), "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534"} {
		if !slices.Contains(args, want) {
			t.Errorf("missing %s in %v", want, args)
		}
	}
}

func TestContainerCapturedExportArchiveBudget(t *testing.T) {
	baseline, err := exportCapacityFixture(t, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, extra := range []int64{0, -1} {
		limit := int64(len(baseline)) + extra
		output, err := exportCapacityFixture(t, limit)
		if extra == 0 && (err != nil || len(output) != len(baseline)) {
			t.Fatalf("exact export: bytes=%d error=%v", len(output), err)
		}
		if extra != 0 && !errors.Is(err, ErrInvalidStepExecution) {
			t.Fatalf("excessive export accepted: bytes=%d error=%v", len(output), err)
		}
		if int64(len(output)) > limit+1 {
			t.Fatal("export exceeded its overflow sentinel")
		}
	}
}

func exportCapacityFixture(t *testing.T, limit int64) ([]byte, error) {
	t.Helper()
	policy := resourcepolicy.Defaults()
	policy.Staging = resourcepolicy.Staging{PayloadBytes: 6, Entries: 1, ArchiveBytes: limit}
	runner, _ := capacityContainerFixture(t, agentproto.RuntimePodman, policy)
	stage, err := runner.NewStage(t.Context(), stageTestArchive(t))
	if stage != nil {
		t.Cleanup(func() { cleanupCapacityStage(t, stage) })
	}
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = stage.Export(t.Context(), &output)
	if closeErr := stage.Close(t.Context()); closeErr != nil {
		t.Fatal(closeErr)
	}
	return output.Bytes(), err
}

func cleanupCapacityStage(t *testing.T, stage *ContainerStage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	if err := stage.Close(ctx); err != nil {
		t.Error(err)
	}
}

func TestContainerResourceCapacityRejectsBeforeEffects(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("TMPDIR", temporary)
	for _, field := range []string{"staging", "artifact", "overhead"} {
		policy := resourcepolicy.Defaults()
		switch field {
		case "staging":
			policy.Staging.Entries = int64(^uint(0)>>1) - 1
		case "artifact":
			policy.Artifact.StagingNodes = int64(^uint(0)>>1) - 1
		case "overhead":
			policy.Container.HelperMemoryOverheadBytes = int64(^uint(0)>>1) - 1
		}
		if _, err := NewContainerExecutorWithResourcePolicy(t.Context(), ContainerConfig{}, policy); err == nil || !strings.Contains(err.Error(), "overflows") {
			t.Fatalf("invalid %s capacity reached runtime: %v", field, err)
		}
	}
	entries, err := os.ReadDir(temporary)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid policy created private runtime state: %v %v", entries, err)
	}
}
