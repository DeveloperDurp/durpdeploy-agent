package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestTerminalDelivery_replays_cancellation_after_rejected_upload(
	t *testing.T,
) {
	// Given: cancel execution on the first log batch, then reject its acknowledgement.
	fixture := newAgentSubprocessFixture(t, "exit 0")
	fixture.cancelledStatus = http.StatusBadRequest
	client, directory := claimLifecycleClient(t, fixture)
	if err := os.WriteFile(
		filepath.Join(directory, "run-cancel"),
		nil,
		0600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	fixture.logsReceived = cancel
	claim, err := client.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// When
	var statusErr *agentclient.StatusError
	if err := executeClaim(
		ctx,
		client,
		*claim,
	); !errors.As(err, &statusErr) ||
		statusErr.Status != http.StatusBadRequest {
		t.Fatalf("cancellation upload: %v", err)
	}
	client.Close()
	// Then
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); err != nil {
		t.Fatalf("unacknowledged cancellation lost: %v", err)
	}
	fixture.mu.Lock()
	fixture.cancelledStatus = 0
	fixture.mu.Unlock()
	received := make(chan agentproto.CancelledRequest, 1)
	fixture.cancelledReceived = func(request agentproto.CancelledRequest) { received <- request }
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	recoveryCtx, recoveryCancel := context.WithTimeout(
		t.Context(),
		5*time.Second,
	)
	defer recoveryCancel()
	if err := resumeCleanupResult(recoveryCtx, restarted); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-received:
		if request.Protocol != agentproto.AgentV3 ||
			request.ClaimToken != claim.ClaimToken {
			t.Fatalf("cancellation replay: %+v", request)
		}
	default:
		t.Fatal("cancellation not replayed to its original endpoint")
	}
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("acknowledged cancellation remains: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.cancelledRequests != 2 || fixture.resultRequests != 0 {
		t.Fatalf(
			"wrong lifecycle replay: cancelled=%d, result=%d",
			fixture.cancelledRequests,
			fixture.resultRequests,
		)
	}
}
