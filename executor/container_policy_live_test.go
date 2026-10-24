//go:build containertest

package executor

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func TestContainerLive_configured_resource_policy(t *testing.T) {
	for _, policy := range containerPolicies() {
		t.Run(fmt.Sprint(policy.PIDs), func(t *testing.T) {
			testLiveContainerPolicy(t, policy)
		})
	}
}

func testLiveContainerPolicy(t *testing.T, policy resourcepolicy.Container) {
	t.Helper()
	runner := liveContainerRunnerWithPolicy(t, policy)
	var logs []string
	script := fmt.Sprintf(`import errno, os, subprocess, threading
limit, capacity = %d, %d
assert int(open('/sys/fs/cgroup/pids.max').read()) == limit
assert os.getuid() == 65534
assert os.statvfs('/tmp').f_blocks * os.statvfs('/tmp').f_frsize == capacity
children = []
try:
    while len(children) < limit:
        try:
            children.append(subprocess.Popen(['sleep', '30']))
        except OSError as error:
            assert error.errno == errno.EAGAIN
            break
    assert len(children) == limit - 1, len(children)
    print('process-budget', len(children), flush=True)
finally:
    for child in children:
        child.terminate()
    for child in children:
        child.wait()
threads, release = [], threading.Event()
try:
    while len(threads) < limit:
        thread = threading.Thread(target=release.wait)
        try:
            thread.start()
        except RuntimeError:
            break
        threads.append(thread)
    assert len(threads) == limit - 1, len(threads)
    print('thread-budget', len(threads), flush=True)
finally:
    release.set()
    for thread in threads:
        thread.join()
written = 0
with open('/tmp/fill', 'wb', buffering=0) as output:
    try:
        while written <= capacity:
            written += output.write(b'x' * 4096)
    except OSError as error:
        assert error.errno == errno.ENOSPC
assert 0 < written <= capacity, written
print('tmpfs-budget', written, flush=True)
os.remove('/tmp/fill')
`, policy.PIDs, policy.TmpfsBytes)
	if err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(JobConfig{
		ScriptBody: script, Interpreter: InterpreterPython3,
		ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "docker.io/library/python:3.12-alpine",
		Timeout: time.Minute,
	}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error { logs = append(logs, line); return nil }})); err != nil {
		t.Fatalf("configured workload: %v, logs=%v", err, logs)
	}
	for _, marker := range []string{fmt.Sprintf("process-budget %d", policy.PIDs-1), fmt.Sprintf("thread-budget %d", policy.PIDs-1), "tmpfs-budget"} {
		if !strings.Contains(strings.Join(logs, "\n"), marker) {
			t.Fatalf("missing %q: %v", marker, logs)
		}
	}
	for _, readOnly := range []bool{false, true} {
		testLiveHelperPolicy(t, runner, policy, readOnly)
	}
	assertLiveArtifactResourcesGone(t, runner)
}

func testLiveHelperPolicy(t *testing.T, runner *ContainerExecutor, policy resourcepolicy.Container, readOnly bool) {
	t.Helper()
	stage, err := runner.newStage(t.Context(), stageTestArchive(t), readOnly)
	if stage != nil {
		t.Cleanup(func() { closeLiveArtifact(t, stage) })
	}
	if err != nil {
		t.Fatal(err)
	}
	raw, err := runner.command(t.Context(), "inspect", stage.keeper).Output()
	if err != nil {
		t.Fatal(err)
	}
	var containers []struct {
		HostConfig struct {
			Memory, NanoCpus, CpuQuota, CpuPeriod, PidsLimit int64
			ReadonlyRootfs                                   bool
			NetworkMode                                      string
		}
		Mounts []struct {
			Destination string
			RW          bool
		}
	}
	if err := json.Unmarshal(raw, &containers); err != nil || len(containers) != 1 {
		t.Fatalf("inspect helper: %s %v", raw, err)
	}
	config := containers[0].HostConfig
	capacity := int64(512<<20) + 20000*int64(os.Getpagesize())
	if readOnly {
		capacity = int64(512<<20) + 19999*int64(os.Getpagesize())
	}
	if config.Memory != capacity+policy.HelperMemoryOverheadBytes || config.PidsLimit != policy.HelperPIDs || !config.ReadonlyRootfs || config.NetworkMode != "none" {
		t.Fatalf("helper policy: %+v", config)
	}
	if config.NanoCpus != int64(policy.HelperCPUs*1e9) && (config.CpuPeriod == 0 || config.CpuQuota != int64(policy.HelperCPUs*float64(config.CpuPeriod))) {
		t.Fatalf("helper CPU policy: %+v", config)
	}
	if !slices.ContainsFunc(containers[0].Mounts, func(mount struct {
		Destination string
		RW          bool
	}) bool {
		return mount.Destination == "/stage" && mount.RW != readOnly
	}) {
		t.Fatalf("missing expected stage mount access: %+v", containers[0].Mounts)
	}
	closeLiveArtifact(t, stage)
}
