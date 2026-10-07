package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestTerminalDelivery_replays_actual_result_after_outage_without_execution(
	t *testing.T,
) {
	for _, fail := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "succeeded", true: "failed"}[fail],
			func(t *testing.T) {
				// Given
				fixture := newAgentSubprocessFixture(t, "exit 0")
				fixture.resultFailures = 100
				client, directory := claimLifecycleClient(t, fixture)
				if fail {
					if err := os.WriteFile(
						filepath.Join(directory, "run-fail"),
						nil,
						0600,
					); err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(
					t.Context(),
					5*time.Second,
				)
				defer cancel()
				fixture.resultRejected = cancel
				claim, err := client.Poll(ctx)
				if err != nil {
					t.Fatal(err)
				}
				// When: work completes but the result endpoint never acknowledges it.
				if err := executeClaim(
					ctx,
					client,
					*claim,
				); !errors.Is(
					err,
					context.Canceled,
				) {
					t.Fatalf("result outage: %v", err)
				}
				client.Close()
				// Then: retain authentication and replay the exact outcome after restart.
				if _, err := os.Stat(
					filepath.Join(fixture.stateDir, claimFileName),
				); err != nil {
					t.Fatalf(
						"unacknowledged result lost authentication: %v",
						err,
					)
				}
				fixture.assertNoSecretFiles(t)
				fixture.mu.Lock()
				fixture.resultFailures = 0
				fixture.mu.Unlock()
				restarted, err := agentclient.NewPaired(
					fixture.stateDir,
					"test",
				)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(restarted.Close)
				// Container opt-in is disabled on this client. Its default protocol must
				// not replace the completed claim's persisted agent/3 protocol.
				recoveryCtx, recoveryCancel := context.WithTimeout(
					t.Context(),
					5*time.Second,
				)
				defer recoveryCancel()
				done := make(chan error, 1)
				go func() { done <- runPaired(recoveryCtx, restarted) }()
				select {
				case result := <-fixture.result:
					want := agentproto.ResultSucceeded
					if fail {
						want = agentproto.ResultFailed
					}
					if result.State != want ||
						result.Protocol != agentproto.AgentV3 ||
						result.ClaimToken != claim.ClaimToken {
						t.Fatalf("replayed result: %+v", result)
					}
				case <-recoveryCtx.Done():
					t.Fatal("result not replayed after restart")
				}
				select {
				case <-fixture.pollAgain:
				case <-recoveryCtx.Done():
					t.Fatal("polling did not resume after ACK")
				}
				recoveryCancel()
				if err := <-done; !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, err := os.Stat(
					filepath.Join(fixture.stateDir, claimFileName),
				); !errors.Is(
					err,
					os.ErrNotExist,
				) {
					t.Fatalf("acknowledged result retained: %v", err)
				}
				runs, err := os.ReadFile(filepath.Join(directory, "ran"))
				if err != nil || string(runs) != "run\n" {
					t.Fatalf("recovery repeated execution: %q, %v", runs, err)
				}
			},
		)
	}
}
