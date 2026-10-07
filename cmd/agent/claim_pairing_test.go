package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
)

func TestCleanupAcknowledgement_rejects_changed_pairing(t *testing.T) {
	// Given: delivery was acknowledged under the original pairing.
	fixture := newAgentSubprocessFixture(t, "exit 0")
	client, directory := claimLifecycleClient(t, fixture)
	if err := persistClaim(client, agentproto.PollResponse{
		DeploymentID: 42, ClaimToken: "test-claim",
	}, claimCleanupAcknowledged); err != nil {
		t.Fatal(err)
	}
	client.Close()
	store := agentstate.NewStore(fixture.stateDir)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	state.AgentID = "another-pairing"
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.EnableContainers(t.Context(), executor.ContainerConfig{
		Runtime:   agentproto.RuntimeDocker,
		SocketURL: "unix://" + filepath.Join(directory, "runtime.sock"),
	}); err != nil {
		t.Fatal(err)
	}
	// When
	if err := resumeCleanupResult(t.Context(), restarted); err == nil {
		t.Fatal("acknowledged report bypassed pairing authentication")
	}
	// Then
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); err != nil {
		t.Fatalf("original recovery obligation lost: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resultRequests != 0 {
		t.Fatal("acknowledged report replayed to changed pairing")
	}
}
