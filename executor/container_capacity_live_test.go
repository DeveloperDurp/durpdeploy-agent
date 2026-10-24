//go:build containertest

package executor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestContainerLive_configured_staging_capacity(t *testing.T) {
	for _, policy := range capacityPolicies() {
		t.Run(fmt.Sprint(policy.Staging.PayloadBytes), func(t *testing.T) {
			runner := liveContainerRunnerWithResourcePolicy(t, policy)
			for _, readOnly := range []bool{false, true} {
				testLiveCapacityStage(t, runner, policy, readOnly)
			}
			assertLiveArtifactResourcesGone(t, runner)
		})
	}
}

func testLiveCapacityStage(t *testing.T, runner *ContainerExecutor, policy resourcepolicy.Limits, readOnly bool) {
	t.Helper()
	create := runner.NewStage
	if readOnly {
		create = runner.NewArtifactStage
	}
	stage, err := create(t.Context(), stageTestArchive(t))
	if stage != nil {
		t.Cleanup(func() { closeLiveArtifact(t, stage) })
	}
	if err != nil {
		t.Fatal(err)
	}
	capacity, nodes := inspectLiveCapacity(t, runner, stage, policy, readOnly)
	testLiveCapacityMount(t, runner, stage, capacity, nodes, readOnly)
	if !readOnly {
		var archive bytes.Buffer
		if err := stage.Export(t.Context(), &archive); err != nil {
			t.Fatal(err)
		}
		if err := staging.UnpackWithPolicy(t.Context(), &archive, "", policy.Staging); err != nil {
			t.Fatal(err)
		}
	}
	closeLiveArtifact(t, stage)
}

func inspectLiveCapacity(t *testing.T, runner *ContainerExecutor, stage *ContainerStage, policy resourcepolicy.Limits, readOnly bool) (int64, int64) {
	t.Helper()
	payloadBytes, nodes := policy.Staging.PayloadBytes, policy.Staging.Entries
	if readOnly {
		payloadBytes, nodes = policy.Artifact.ExtractedBytes, policy.Artifact.StagingNodes
	}
	capacity := payloadBytes + nodes*int64(os.Getpagesize())
	raw, err := runner.command(t.Context(), "inspect", stage.keeper).Output()
	if err != nil {
		t.Fatal(err)
	}
	var keepers []struct {
		HostConfig struct{ Memory, PidsLimit int64 }
	}
	if err := json.Unmarshal(raw, &keepers); err != nil || len(keepers) != 1 {
		t.Fatalf("inspect capacity helper: %s %v", raw, err)
	}
	if keepers[0].HostConfig.Memory != capacity+policy.Container.HelperMemoryOverheadBytes || keepers[0].HostConfig.PidsLimit != policy.Container.HelperPIDs {
		t.Fatalf("helper capacity differs: %+v", keepers)
	}
	volume, err := runner.command(t.Context(), "volume", "inspect", stage.volume).Output()
	if err != nil {
		t.Fatal(err)
	}
	var volumes []struct{ Options map[string]string }
	if err := json.Unmarshal(volume, &volumes); err != nil || len(volumes) != 1 {
		t.Fatalf("inspect capacity volume: %s %v", volume, err)
	}
	for _, want := range []string{fmt.Sprintf("size=%d", capacity), fmt.Sprintf("nr_inodes=%d", 2*(nodes+1)), "noexec", "nosuid", "nodev"} {
		if !strings.Contains(volumes[0].Options["o"], want) {
			t.Errorf("missing %s in %+v", want, volumes)
		}
	}
	return capacity, nodes
}

func testLiveCapacityMount(t *testing.T, runner *ContainerExecutor, stage *ContainerStage, capacity, nodes int64, readOnly bool) {
	t.Helper()
	job := JobConfig{Interpreter: InterpreterPython3, ExecutionMode: agentproto.ExecutionContainer,
		ContainerImage: "docker.io/library/python:3.12-alpine", StageVolume: stage.Volume, Timeout: time.Minute}
	path := "/stage"
	if readOnly {
		job.StageVolume, job.ArtifactVolume, path = "", stage.Volume, "/artifacts"
	}
	job.ScriptBody = fmt.Sprintf(`import errno, os
p = %q
s = os.statvfs(p)
assert s.f_blocks * s.f_frsize == %d, (s.f_blocks, s.f_frsize)
assert s.f_files == %d, s.f_files
assert open(p+'/input').read() == 'pinned'
print('volume-capacity', s.f_blocks * s.f_frsize, s.f_files, flush=True)
`, path, capacity, 2*(nodes+1))
	if readOnly {
		job.ScriptBody += `try:
    open(p+'/forbidden','w')
    raise AssertionError('artifact mount is writable')
except OSError as error:
    assert error.errno == errno.EROFS, error
`
	} else if capacity < 2<<20 {
		job.ScriptBody += `written = 0
with open(p+'/fill','wb',buffering=0) as output:
    try:
        while written <= s.f_blocks * s.f_frsize:
            written += output.write(b'x'*4096)
    except OSError as error:
        assert error.errno == errno.ENOSPC, error
assert 0 < written <= s.f_blocks * s.f_frsize, written
os.remove(p+'/fill')
print('volume-exhaustion', written, flush=True)
`
	}
	var logs []string
	if err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(job), NewCallbacks(CallbacksConfig{
		WriteLog: func(line string) error { logs = append(logs, line); return nil },
	})); err != nil {
		t.Fatalf("capacity workload: %v, logs=%v", err, logs)
	}
	if !strings.Contains(strings.Join(logs, "\n"), "volume-capacity") {
		t.Fatalf("missing capacity marker: %v", logs)
	}
}

func TestContainerLive_configured_export_overflow(t *testing.T) {
	policy := resourcepolicy.Defaults()
	policy.Staging = resourcepolicy.Staging{PayloadBytes: 6, Entries: 1, ArchiveBytes: 2048}
	runner := liveContainerRunnerWithResourcePolicy(t, policy)
	stage, err := runner.NewStage(t.Context(), stageTestArchive(t))
	if stage != nil {
		t.Cleanup(func() { closeLiveArtifact(t, stage) })
	}
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := stage.Export(t.Context(), &output); err == nil || int64(output.Len()) > policy.Staging.ArchiveBytes+1 {
		t.Fatalf("export overflow: bytes=%d error=%v", output.Len(), err)
	}
	closeLiveArtifact(t, stage)
	assertLiveArtifactResourcesGone(t, runner)
}
