package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestRunPaired_replays_encrypted_cleanup_before_polling_after_restart(
	t *testing.T,
) {
	testCleanupReplay(t, "", claimCleanupPending)
}

func TestCleanupReplay_rejects_changed_runtime_endpoint(t *testing.T) {
	for _, change := range []string{"socket", "runtime"} {
		t.Run(
			change,
			func(t *testing.T) { testCleanupReplay(t, change, claimCleanupPending) },
		)
		t.Run(
			change+"-acknowledged",
			func(t *testing.T) { testCleanupReplay(t, change, claimCleanupAcknowledged) },
		)
	}
}

func testCleanupReplay(t *testing.T, change string, phase claimPhase) {
	t.Helper()
	// Given
	fixture := newAgentSubprocessFixture(t, "exit 0")
	fixture.pollServed = true
	fixture.resultFailures = 1
	state, err := agentstate.New(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverID.Fingerprint},
		"agent-test",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(fixture.stateDir).Save(state); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	socket := filepath.Join(directory, "runtime.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := os.WriteFile(
		filepath.Join(directory, "docker"),
		[]byte(`#!/bin/bash
while [[ "$1" == --* ]]; do shift; done
case "$1" in
info) printf '%s' '{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=builtin"],"host":{"os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true,"seccompProfilePath":"/usr/share/containers/seccomp.json"}}}' ;;
ps) ;;
*) exit 2 ;;
esac
`),
		0755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	config := executor.ContainerConfig{
		Runtime:   agentproto.RuntimeDocker,
		SocketURL: "unix://" + socket,
	}
	client, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.EnableContainers(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	if err := persistClaim(
		client,
		agentproto.PollResponse{DeploymentID: 42, ClaimToken: "test-claim"},
		phase,
	); err != nil {
		t.Fatal(err)
	}
	fixture.assertNoSecretFiles(t)
	client.Close()
	switch change {
	case "socket":
		otherSocket := filepath.Join(directory, "other.sock")
		other, err := net.Listen("unix", otherSocket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := other.Close(); err != nil {
				t.Error(err)
			}
		})
		config.SocketURL = "unix://" + otherSocket
	case "runtime":
		binary, err := os.ReadFile(filepath.Join(directory, "docker"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(directory, "podman"),
			binary,
			0755,
		); err != nil {
			t.Fatal(err)
		}
		config.Runtime = agentproto.RuntimePodman
	}
	restarted, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if err := restarted.EnableContainers(t.Context(), config); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if change != "" {
		// When
		err := resumeCleanupResult(ctx, restarted)
		// Then
		if !errors.Is(err, executor.ErrContainerCleanup) {
			t.Fatalf("changed endpoint accepted recovery: %v", err)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if fixture.resultRequests != 0 {
			t.Fatal("sent report after reconciling a different runtime")
		}
		return
	}
	done := make(chan error, 1)
	// When
	go func() { done <- runPaired(ctx, restarted) }()
	select {
	case result := <-fixture.result:
		if result.State != agentproto.ResultCleanupUnconfirmed {
			t.Fatalf("result: %+v", result)
		}
	case <-ctx.Done():
		t.Fatal("pending result was not retried")
	}
	select {
	case <-fixture.pollAgain:
	case <-ctx.Done():
		t.Fatal("polling did not resume after delivery")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// Then
	if _, err := os.Stat(
		filepath.Join(fixture.stateDir, claimFileName),
	); !errors.Is(
		err,
		os.ErrNotExist,
	) {
		t.Fatalf("delivered marker remains: %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.resultRequests != 2 {
		t.Fatalf("result attempts: %d", fixture.resultRequests)
	}
}
