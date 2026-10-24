package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/configuration"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"go.yaml.in/yaml/v3"
)

func TestAgentSubprocess_configuredResourcePolicy(t *testing.T) {
	for _, scenario := range []string{"raised", "lowered-invalid", "default-timeout", "missing-ack"} {
		t.Run(scenario, func(t *testing.T) { checkSubprocessResourcePolicy(t, scenario) })
	}
}

func checkSubprocessResourcePolicy(t *testing.T, scenario string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "executed")
	fixture := newAgentSubprocessFixture(t, `touch "$MARKER"; echo configured-policy-executed`)
	fixture.addVariable(variablePayload{Name: "MARKER", Value: marker})
	settings := configuration.Defaults()
	settings.Limits.Execution.MaxRetries = 12
	settings.Limits.Container.PIDs = 24
	step := &fixture.payload.Release.Steps[0]
	step.MaxRetries = 12
	if scenario == "lowered-invalid" {
		settings.Limits.Execution.MaxRetries = 0
		step.MaxRetries = 1
	}
	if scenario == "default-timeout" {
		settings.Limits.Execution.DefaultTimeoutSeconds = 1
		settings.Limits.Execution.MaxTimeoutSeconds = 10
		step.TimeoutSeconds, step.MaxRetries, step.ScriptBody = 0, 0, "exec sleep 5"
	}
	var starts atomic.Int32
	fixture.startAcknowledged = func() { starts.Add(1) }
	if scenario != "missing-ack" {
		fixture.server.Config.Handler = withClaimPolicy(t, settings.Limits, http.HandlerFunc(fixture.handle))
	}
	raw, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	file := startupConfigFile(t, string(raw))
	process := fixture.start(t, "DURPDEPLOY_AGENT_CONFIG_FILE="+file)
	stop := sync.OnceFunc(func() {
		if err := process.Process.Signal(syscall.SIGTERM); err != nil {
			t.Error(err)
		}
		if err := process.Wait(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(stop)
	if scenario == "lowered-invalid" || scenario == "missing-ack" {
		checkRejectedSubprocessPolicy(t, fixture, &starts, marker, scenario, stop)
		return
	}
	checkSubprocessPolicyResult(t, fixture, &starts, scenario, stop)
	stop()
	if scenario == "raised" {
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("raised policy snapshot did not execute")
		}
	}
}

func checkRejectedSubprocessPolicy(t *testing.T, fixture *agentSubprocessFixture, starts *atomic.Int32, marker, scenario string, stop func()) {
	t.Helper()
	select {
	case <-fixture.pollAgain:
	case <-time.After(10 * time.Second):
		t.Fatal("rejected claim did not resume polling")
	}
	stop()
	if starts.Load() != 0 || len(fixture.logs()) != 0 {
		t.Fatal("incompatible or invalid claim reached execution")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("rejected claim executed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture.stateDir, claimFileName)); !os.IsNotExist(err) {
		t.Fatalf("rejected claim retained marker: %v", err)
	}
	if scenario == "missing-ack" && !strings.Contains(fixture.stderr.String(), "incompatible agent resource policy") {
		t.Fatal("missing policy diagnostic")
	}
}

func checkSubprocessPolicyResult(t *testing.T, fixture *agentSubprocessFixture, starts *atomic.Int32, scenario string, stop func()) {
	t.Helper()
	select {
	case result := <-fixture.result:
		want := agentproto.ResultSucceeded
		if scenario == "default-timeout" {
			want = agentproto.ResultFailed
		}
		if result.State != want || starts.Load() != 1 {
			t.Fatalf("configured result=%+v starts=%d", result, starts.Load())
		}
		if scenario == "default-timeout" && !strings.HasPrefix(result.Error, "step_timeout:") {
			t.Fatalf("zero timeout did not use captured deadline: %+v", result)
		}
	case <-time.After(30 * time.Second):
		stop()
		t.Logf("configured agent diagnostic: %s", fixture.stderr.String())
		t.Fatal("configured execution did not finish")
	}
}
