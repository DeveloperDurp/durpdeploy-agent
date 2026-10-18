//go:build containertest

package executor

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func liveArtifactTAR(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range []struct {
		name, data string
		mode       int64
	}{
		{"input", "pinned", 0644},
		{"tool.sh", "#!/bin/sh\necho artifact-tool\n", 0755},
	} {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode,
			Uid: 65534, Gid: 65534, Size: int64(len(entry.data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(writer, entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return &buffer
}

func closeLiveArtifact(t *testing.T, stage *ContainerStage) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 30*time.Second)
	defer cancel()
	if err := stage.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func assertLiveArtifactResourcesGone(t *testing.T, runner *ContainerExecutor) {
	t.Helper()
	containers, err := runner.ownedContainers(t.Context())
	if err != nil || len(containers) != 0 {
		t.Fatalf("retained containers: %v %v", containers, err)
	}
	volumes, err := runner.command(t.Context(), "volume", "ls", quietFlag,
		"--filter=label=io.durpdeploy.agentstage="+runner.namespace).Output()
	if err != nil || len(strings.Fields(string(volumes))) != 0 {
		t.Fatalf("retained volumes: %s %v", volumes, err)
	}
}

func TestContainerLive_artifact_mount_retries_and_cleanup(t *testing.T) {
	runner := liveContainerRunner(t)
	artifact, err := runner.NewArtifactStage(t.Context(), liveArtifactTAR(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeLiveArtifact(t, artifact) })
	stage, err := runner.NewStage(t.Context(), stageTestArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { closeLiveArtifact(t, stage) })
	remaining, err := runner.command(t.Context(), "ps", "--all", "--quiet", "--filter=name="+artifact.importer).Output()
	if err != nil || len(strings.Fields(string(remaining))) != 0 {
		t.Fatalf("writable importer remains: %s %v", remaining, err)
	}
	// Read-only mounts must prohibit mutation even when the files are owned by
	// the step user; an executable fixture independently proves noexec.
	script := `set -eu
test "$SELECTED" = yes
test "${HIDDEN-unset}" = unset
test "$(cat "$ARTIFACT_PATH/input")" = pinned
opts=$(awk '$5 == "/artifacts" {print $6}' /proc/self/mountinfo)
for flag in ro noexec nosuid nodev; do
 case ",$opts," in *,$flag,*) ;; *) echo "missing mount flag $flag: $opts"; exit 17;; esac
done
if touch "$ARTIFACT_PATH/new"; then echo 'artifact create succeeded'; exit 20; fi
if sh -c 'echo changed > "$ARTIFACT_PATH/input"'; then echo 'artifact overwrite succeeded'; exit 21; fi
if chmod 0777 "$ARTIFACT_PATH/tool.sh"; then echo 'artifact chmod succeeded'; exit 22; fi
test "$(stat -c %a "$ARTIFACT_PATH/tool.sh")" = 755
if "$ARTIFACT_PATH/tool.sh"; then echo 'artifact execution succeeded'; exit 23; fi
test "$(sh "$ARTIFACT_PATH/tool.sh")" = artifact-tool
cp "$ARTIFACT_PATH/tool.sh" /tmp/copied.sh
chmod +x /tmp/copied.sh
test "$(sh /tmp/copied.sh)" = artifact-tool
# Docker may also mount /tmp noexec. Only execute a copy where that mount allows it.
tmpopts=$(awk '$5 == "/tmp" {print $6}' /proc/self/mountinfo)
case ",$tmpopts," in *,noexec,*) ;; *) test "$(/tmp/copied.sh)" = artifact-tool;; esac
test ! -e /run/durpdeploy/runtime.sock
grep -q '^CapEff:.*0000000000000000' /proc/self/status
grep -q '^NoNewPrivs:.*1' /proc/self/status
echo artifact-attempt
if [ ! -e "$DURPDEPLOY_STAGE_DIR/retried" ]; then
 touch "$DURPDEPLOY_STAGE_DIR/retried"
 exit 9
fi
echo artifact-retry-ok`
	var logs []string
	err = NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(JobConfig{
		ArtifactVolume: artifact.Volume, StageVolume: stage.Volume,
		ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "docker.io/library/bash:5.2",
		VariableNames: []string{"SELECTED"}, Environment: map[string]string{"SELECTED": "yes", "HIDDEN": "private"},
		ScriptBody: script, MaxRetries: 1, Timeout: time.Minute,
	}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error { logs = append(logs, line); return nil }}))
	if err != nil {
		t.Fatalf("artifact retry failed: %v\n%s", err, strings.Join(logs, "\n"))
	}
	output := strings.Join(logs, "\n")
	if strings.Count(output, "artifact-attempt") != 2 || !strings.Contains(output, "artifact-retry-ok") {
		t.Fatalf("retry did not reuse package: %s", output)
	}
	closeLiveArtifact(t, artifact)
	closeLiveArtifact(t, stage)
	assertLiveArtifactResourcesGone(t, runner)
	t.Log("effective ro,noexec,nosuid,nodev; executable-bit direct execution rejected; interpreter/copy permitted; both retries read pinned bytes; no resources retained")
}

func TestContainerLive_artifact_failure_cancellation_and_restart(t *testing.T) {
	for _, scenario := range []string{"failure", "cancel", "restart", "partial-import"} {
		t.Run(scenario, func(t *testing.T) {
			runner := liveContainerRunner(t)
			if scenario == "partial-import" {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				_, err := runner.NewArtifactStage(ctx, &cancelArtifactImport{liveArtifactTAR(t), cancel})
				if err == nil {
					t.Fatal("cancelled import succeeded")
				}
				assertLiveArtifactResourcesGone(t, runner)
				return
			}
			artifact, err := runner.NewArtifactStage(t.Context(), liveArtifactTAR(t))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { closeLiveArtifact(t, artifact) })
			if scenario == "restart" {
				// Emulate a crash during handover with both helpers still alive.
				if err := artifact.startKeeper(t.Context(), artifact.importer, "rw", 64<<20); err != nil {
					t.Fatal(err)
				}
				restarted, err := NewContainerExecutor(t.Context(), ContainerConfig{
					Runtime: runner.runtime, SocketURL: runner.socketURL,
					AgentID: agentproto.AgentID(t.Name()), PairingIdentity: "live-contract-server/live-contract-certificate",
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := restarted.Close(); err != nil {
						t.Error(err)
					}
				})
				assertLiveArtifactResourcesGone(t, restarted)
				return
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			script := "echo artifact-ready; exit 7"
			if scenario == "cancel" {
				script = "echo artifact-ready; sleep 30"
			}
			err = NewExecutorWithContainers(runner).Execute(ctx, NewJob(JobConfig{
				ArtifactVolume: artifact.Volume, ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "docker.io/library/bash:5.2", ScriptBody: script, Timeout: time.Minute,
			}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
				if scenario == "cancel" && line == "artifact-ready" {
					cancel()
				}
				return nil
			}}))
			if err == nil || (scenario == "cancel" && !errors.Is(err, context.Canceled)) {
				t.Fatalf("unexpected %s result: %v", scenario, err)
			}
			closeLiveArtifact(t, artifact)
			assertLiveArtifactResourcesGone(t, runner)
		})
	}
}

type cancelArtifactImport struct {
	source io.Reader
	cancel context.CancelFunc
}

func (r *cancelArtifactImport) Read(data []byte) (int, error) {
	r.cancel()
	return r.source.Read(data)
}
