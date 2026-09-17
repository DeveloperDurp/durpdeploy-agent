package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
	"github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestRun_reconnectsAfterRejectedPoll(t *testing.T) {
	fixture := newAgentSubprocessFixture(t, "")
	fixture.payload.Release.Steps = nil
	fixture.pollBadRequestOnce = true
	paired, err := agentstate.New(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverID.Fingerprint},
		"agent-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(fixture.stateDir).Save(paired); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, config{
			stateDir:     fixture.stateDir,
			agentVersion: agentproto.AgentVersion("test"),
		})
	}()

	select {
	case result := <-fixture.result:
		if result.State != agentproto.ResultSucceeded {
			t.Fatalf("result state = %q", result.State)
		}
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("agent did not recover after rejected poll")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v", err)
	}
}

func TestWaitForRetry_stopsAfterShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if waitForRetry(ctx) {
		t.Fatal("retry continued after shutdown")
	}
}
