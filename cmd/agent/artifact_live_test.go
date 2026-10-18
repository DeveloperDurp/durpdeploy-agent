//go:build containertest

package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentContainerLive_package_verification_retry_and_cleanup(t *testing.T) {
	for _, scenario := range []string{"retry", "corrupt-bytes", "invalid-zip", "failure", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			runtimePath := os.Getenv("PATH")
			fixture := newClaimPackageFixture(t, scenario)
			fixture.client.Close()
			t.Setenv("PATH", runtimePath)
			client, err := agentclient.NewPaired(fixture.process.stateDir, "package-live")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(client.Close)
			fixture.client = client
			runtime, err := agentproto.ParseContainerRuntime(os.Getenv("AGENT_TEST_RUNTIME"))
			if err != nil {
				t.Fatal(err)
			}
			if err := client.EnableContainers(t.Context(), executor.ContainerConfig{
				Runtime: runtime, SocketURL: os.Getenv("AGENT_TEST_SOCKET"),
			}); err != nil {
				t.Fatal(err)
			}
			identity, err := json.Marshal([]string{"agent-test", fixture.process.server.URL + "\x00" + fixture.process.identity.Fingerprint.String()})
			if err != nil {
				t.Fatal(err)
			}
			namespace := fmt.Sprintf("agent-%x", sha256.Sum256(identity))
			checkCleanup := func() {
				if err := livePackageRuntimeEmpty(t.Context(), runtime, namespace); err != nil {
					t.Error(err)
				}
				entries, err := os.ReadDir(filepath.Join(fixture.process.stateDir, "staging"))
				if err != nil || len(entries) != 0 {
					t.Errorf("lifecycle acknowledged before file cleanup: %v %v", entries, err)
				}
			}
			fixture.process.resultReceived = func(agentproto.ResultRequest) { checkCleanup() }
			fixture.process.cancelledReceived = func(agentproto.CancelledRequest) { checkCleanup() }
			step := &fixture.process.payload.Release.Steps[0]
			step.ExecutionMode, step.ContainerImage = agentproto.ExecutionContainer, "docker.io/library/bash:5.2"
			step.VariableNames = []string{"SELECTED"}
			step.MaxRetries = 1
			step.ScriptBody = `set -eu
echo package-step-executed
test "$SELECTED" = yes
test "${SECRET-unset}" = unset
test "$(cat "$ARTIFACT_PATH/input")" = release
if touch "$ARTIFACT_PATH/new"; then exit 20; fi
if chmod 777 "$ARTIFACT_PATH/input"; then exit 21; fi
if [ ! -e "$DURPDEPLOY_STAGE_DIR/retried" ]; then
 touch "$DURPDEPLOY_STAGE_DIR/retried"
 exit 9
fi
cp "$ARTIFACT_PATH/input" "$DURPDEPLOY_STAGE_DIR/output"`
			fixture.process.addVariable(variablePayload{Name: "SELECTED", Value: "yes"})
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
			defer cancel()
			if scenario == "failure" {
				step.ScriptBody = "echo package-step-executed; exit 7"
			}
			if scenario == "cancel" {
				step.ScriptBody = "echo package-step-executed; for ((i=0;i<100;i++)); do echo padding; done; sleep 30"
				fixture.process.logsReceived = cancel
			}
			claim, err := client.Poll(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := executeClaim(ctx, client, *claim); err != nil {
				t.Fatal(err)
			}
			if fixture.downloads.Load() != 1 {
				t.Fatalf("package downloaded %d times", fixture.downloads.Load())
			}
			if scenario == "cancel" {
				fixture.process.mu.Lock()
				acks := fixture.process.cancelledRequests
				fixture.process.mu.Unlock()
				if acks != 1 {
					t.Fatalf("cancellation acknowledgements: %d", acks)
				}
			} else {
				result := <-fixture.process.result
				want := agentproto.ResultFailed
				if scenario == "retry" {
					want = agentproto.ResultSucceeded
				}
				if result.State != want {
					t.Fatalf("result=%+v want=%s", result, want)
				}
			}
			logs := strings.Join(fixture.process.logs(), "\n")
			if (scenario == "corrupt-bytes" || scenario == "invalid-zip") && strings.Contains(logs, "package-step-executed") {
				t.Fatalf("invalid package reached a step: %s", logs)
			}
			if scenario == "retry" {
				if strings.Count(logs, "package-step-executed") != 2 {
					t.Fatalf("missing retry: %s", logs)
				}
				fixture.mu.Lock()
				output := append([]byte(nil), fixture.output...)
				fixture.mu.Unlock()
				assertPackagePublication(t, output)
			}
			assertPackageDirectoryEmpty(t, filepath.Join(fixture.process.stateDir, "staging"))
			if err := livePackageRuntimeEmpty(t.Context(), runtime, namespace); err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: one package request; lifecycle acknowledged after managed files, containers and volumes were removed", scenario)
		})
	}
}

func livePackageRuntimeEmpty(ctx context.Context, runtime agentproto.ContainerRuntime, namespace string) error {
	args := []string{"--host=" + os.Getenv("AGENT_TEST_SOCKET")}
	if runtime == agentproto.RuntimePodman {
		args = []string{"--remote", "--url=" + os.Getenv("AGENT_TEST_SOCKET")}
	}
	// Scope inspection to this claim's paired identity so independently running
	// runtime contracts may own unrelated helpers at the same time.
	for _, command := range [][]string{
		{"ps", "--all", "--quiet", "--filter=label=io.durpdeploy.agent=" + namespace},
		{"volume", "ls", "--quiet", "--filter=label=io.durpdeploy.agentstage=" + namespace},
	} {
		checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		output, err := exec.CommandContext(checkCtx, string(runtime), append(args, command...)...).CombinedOutput()
		cancel()
		if err != nil || strings.TrimSpace(string(output)) != "" {
			return fmt.Errorf("runtime resources retained: %s %v", output, err)
		}
	}
	return nil
}
