package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClaimLifecycle_marker_write_failure_reports_without_execution(
	t *testing.T,
) {
	for _, blocked := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "retired", true: "retirement-blocked"}[blocked],
			func(t *testing.T) {
				// Given: the marker cannot be replaced after start is acknowledged.
				fixture := newAgentSubprocessFixture(t, "exit 0")
				client, directory := claimLifecycleClient(t, fixture)
				marker := filepath.Join(fixture.stateDir, claimFileName)
				fixture.startAcknowledged = func() {
					if err := os.Remove(marker); err != nil {
						t.Error(err)
						return
					}
					if err := os.Mkdir(marker, 0700); err != nil {
						t.Error(err)
						return
					}
					if blocked {
						if err := os.WriteFile(
							filepath.Join(marker, "block"),
							nil,
							0600,
						); err != nil {
							t.Error(err)
						}
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				claim, err := client.Poll(ctx)
				if err != nil {
					t.Fatal(err)
				}
				// When
				if err := executeClaim(ctx, client, *claim); err == nil {
					t.Fatal("marker write failure ignored")
				}
				// Then
				select {
				case result := <-fixture.result:
					want := agentproto.ResultFailed
					if blocked {
						want = agentproto.ResultCleanupUnconfirmed
					}
					if result.State != want {
						t.Fatalf("result: %+v, want %s", result, want)
					}
				default:
					t.Fatal("started deployment has no terminal report")
				}
				if _, err := os.Stat(
					filepath.Join(directory, "ran"),
				); !errors.Is(
					err,
					os.ErrNotExist,
				) {
					t.Fatalf("workload started: %v", err)
				}
			},
		)
	}
}

func TestClaimLifecycle_cleanup_ack_retains_original_runtime_until_reconciled(
	t *testing.T,
) {
	// Given
	fixture := newAgentSubprocessFixture(t, "exit 0")
	client, directory := claimLifecycleClient(t, fixture)
	if err := os.WriteFile(
		filepath.Join(directory, "cleanup-fail"),
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
	if err := executeClaim(
		ctx,
		client,
		*claim,
	); !errors.Is(
		err,
		executor.ErrContainerCleanup,
	) {
		t.Fatalf("cleanup: %v", err)
	}
	if result := <-fixture.result; result.State != agentproto.ResultCleanupUnconfirmed {
		t.Fatalf("result: %+v", result)
	}
	client.Close()
	// Then: acknowledgement does not retire the original cleanup obligation.
	raw, err := os.ReadFile(filepath.Join(fixture.stateDir, claimFileName))
	if err != nil {
		t.Fatal(err)
	}
	var marker struct {
		Acknowledged bool `json:"cleanup_acknowledged"`
	}
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatal(err)
	}
	if !marker.Acknowledged {
		t.Fatal("cleanup acknowledgement was not persisted")
	}
	fixture.assertNoSecretFiles(t)
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := resumeCleanupResult(
		ctx,
		restarted,
	); !errors.Is(
		err,
		executor.ErrContainerCleanup,
	) {
		t.Fatalf("disabled containers bypassed recovery: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "cleanup-fail")); err != nil {
		t.Fatal(err)
	}
	if err := restarted.EnableContainers(ctx, executor.ContainerConfig{
		Runtime:   agentproto.RuntimeDocker,
		SocketURL: "unix://" + filepath.Join(directory, "runtime.sock"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := resumeCleanupResult(ctx, restarted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(
		filepath.Join(directory, "owned"),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("owned container remains: %v", err)
	}
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("reconciled marker remains: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resultRequests != 1 {
		t.Fatalf(
			"acknowledged report replayed: %d requests",
			fixture.resultRequests,
		)
	}
}

func TestClaimLifecycle_normal_results_retire_recovery_before_delivery(
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
					if _, err := os.Stat(
						filepath.Join(fixture.stateDir, claimFileName),
					); !errors.Is(
						err,
						os.ErrNotExist,
					) {
						t.Errorf(
							"recovery report exists before %s ACK: %v",
							result.State,
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

func TestClaimLifecycle_retirement_failure_blocks_normal_result(t *testing.T) {
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
		t.Fatal("retirement failure ignored")
	}
	// Then
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resultRequests != 0 {
		t.Fatal("normal result sent with active recovery state")
	}
}
