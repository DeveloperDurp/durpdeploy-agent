package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClaimLifecycle_normal_results_replace_cleanup_report_before_delivery(
	t *testing.T,
) {
	for _, fail := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "succeeded", true: "failed"}[fail],
			func(t *testing.T) {
				// Given
				fixture := newAgentSubprocessFixture(t, "exit 0")
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
				fixture.resultReceived = func(result agentproto.ResultRequest) {
					raw, err := os.ReadFile(
						filepath.Join(fixture.stateDir, claimFileName),
					)
					if err != nil {
						t.Error(err)
						return
					}
					var marker claimMarker
					if err := json.Unmarshal(raw, &marker); err != nil {
						t.Error(err)
						return
					}
					if len(marker.CleanupResult) != 0 {
						t.Error(
							"conservative cleanup report remains before normal ACK",
						)
					}
					saved, err := client.DecodeTerminalReport(
						42,
						marker.TerminalReport,
					)
					if err != nil || saved != result {
						t.Errorf(
							"saved outcome differs from uploaded result: %v, %v",
							saved,
							err,
						)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				claim, err := client.Poll(ctx)
				if err != nil {
					t.Fatal(err)
				}
				// When
				if err := executeClaim(ctx, client, *claim); err != nil {
					t.Fatal(err)
				}
				// Then
				want := agentproto.ResultSucceeded
				if fail {
					want = agentproto.ResultFailed
				}
				if result := <-fixture.result; result.State != want {
					t.Fatalf("result: %+v", result)
				}
			},
		)
	}
}

func TestClaimLifecycle_terminal_persistence_failure_blocks_normal_result(
	t *testing.T,
) {
	// Given
	fixture := newAgentSubprocessFixture(t, "exit 0")
	client, directory := claimLifecycleClient(t, fixture)
	if err := os.WriteFile(
		filepath.Join(directory, "retire-fail"),
		nil,
		0600,
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	claim, err := client.Poll(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// When
	if err := executeClaim(ctx, client, *claim); err == nil {
		t.Fatal("terminal persistence failure ignored")
	}
	// Then
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resultRequests != 0 {
		t.Fatal("normal result sent with active recovery state")
	}
}
