package agentclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPairedExecutionPolicySnapshotAndStepSupport(t *testing.T) {
	dir := t.TempDir()
	identity, err := agenttls.LoadOrCreate(dir, "https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	state, err := agentstate.New("https://127.0.0.1:1", []agenttls.Fingerprint{identity.Fingerprint}, "configured-agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(dir).Save(state); err != nil {
		t.Fatal(err)
	}
	policy := resourcepolicy.Execution{MaxRetries: 12, MaxTimeoutSeconds: 172800, DefaultTimeoutSeconds: 600}
	client, err := NewPairedWithExecutionPolicy(dir, "test", executor.StartupSettings{}, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	policy.MaxRetries = 0
	policyCopy := client.ExecutionPolicy()
	policyCopy.MaxRetries = 0
	if client.ExecutionPolicy().MaxRetries != 12 {
		t.Fatal("client policy is mutable")
	}
	step := executor.Step{Interpreter: executor.InterpreterBash, MaxRetries: 12, TimeoutSeconds: 172800}
	if !client.SupportsStep(step) {
		t.Fatal("raised policy rejected a compatible step")
	}
	step.MaxRetries++
	if client.SupportsStep(step) {
		t.Fatal("client accepted excessive retries")
	}
	legacy, err := NewPaired(dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(legacy.Close)
	if legacy.ExecutionPolicy() != resourcepolicy.Defaults().Execution || legacy.SupportsStep(executor.Step{MaxRetries: 11}) {
		t.Fatal("legacy client did not preserve default limits")
	}
}

func TestPairedExecutionPolicyRejectsBeforeStateEffects(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	if _, err := NewPairedWithExecutionPolicy(dir, "test", executor.StartupSettings{}, resourcepolicy.Execution{}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("invalid policy created state: %v", err)
	}
}
