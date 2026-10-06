package agentclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestClient_v3_poll_rechecks_runtime_before_retry_and_recovers(
	t *testing.T,
) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			directory := t.TempDir()
			socket := filepath.Join(directory, "runtime.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			offline := filepath.Join(directory, "offline")
			script := fmt.Sprintf(`#!/bin/bash
set -eu
while [[ "$1" == --* ]]; do shift; done
case "$1" in
info) [[ ! -e %q ]] || exit 1
printf '%%s' '{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp"],"host":{"os":"linux","cgroupControllers":["cpu","memory","pids"],"security":{"seccompEnabled":true}}}' ;;
ps) ;;
*) exit 2 ;;
esac
`, offline)
			if err := os.WriteFile(
				filepath.Join(directory, string(runtime)),
				[]byte(script),
				0755,
			); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
			identity := testIdentity(t)
			polls := make(chan agentproto.PollRequest, 3)
			var requests atomic.Int32
			server := newTLSServer(
				t,
				identity,
				func(w http.ResponseWriter, r *http.Request) {
					poll, err := agentproto.DecodeRequest[agentproto.PollRequest](
						r.Body,
					)
					if err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					polls <- poll
					if requests.Add(1) == 1 {
						if err := os.WriteFile(offline, nil, 0600); err != nil {
							t.Error(err)
						}
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				},
			)
			client := testClient(t, server.URL, identity.Fingerprint.String())
			client.sleep = func(context.Context, time.Duration) error { return nil }
			if err := client.EnableContainers(
				t.Context(),
				executor.ContainerConfig{
					Runtime:   runtime,
					SocketURL: "unix://" + socket,
				},
			); err != nil {
				t.Fatal(err)
			}
			// When
			claim, err := client.Poll(t.Context())
			// Then
			if claim != nil ||
				!errors.Is(err, executor.ErrContainerUnavailable) ||
				requests.Load() != 1 {
				t.Fatalf(
					"poll during retry outage: %v, %v, requests=%d",
					claim,
					err,
					requests.Load(),
				)
			}
			poll := <-polls
			if poll.Protocol != agentproto.AgentV3 ||
				len(poll.ContainerRuntimes) != 1 ||
				poll.ContainerRuntimes[0] != runtime ||
				len(poll.ContainerInterpreters) != 3 {
				t.Fatalf("capabilities: %+v", poll)
			}
			if err := client.ValidateExecutionReady(
				t.Context(),
			); !errors.Is(
				err,
				executor.ErrContainerUnavailable,
			) {
				t.Fatalf("start readiness during outage: %v", err)
			}
			if err := os.Remove(offline); err != nil {
				t.Fatal(err)
			}
			client.interpreters = nil
			if claim, err := client.Poll(
				t.Context(),
			); err != nil ||
				claim != nil {
				t.Fatalf("poll recovery: %v, %v", claim, err)
			}
			poll = <-polls
			if len(poll.ExecutionModes) != 1 ||
				poll.ExecutionModes[0] != agentproto.ExecutionContainer {
				t.Fatalf("container-only capabilities: %+v", poll)
			}
			if client.ContainerExecutor() == nil ||
				!client.SupportsStep(
					executor.Step{
						Interpreter:    executor.InterpreterPython3,
						ExecutionMode:  agentproto.ExecutionContainer,
						ContainerImage: "image",
					},
				) {
				t.Fatal(
					"container entrypoint depends on host interpreter availability",
				)
			}
			if client.SupportsStep(
				executor.Step{ExecutionMode: agentproto.ExecutionContainer},
			) {
				t.Fatal("accepted missing image")
			}
			client.Close()
		})
	}
}

func TestClient_legacy_capabilities_reject_v3_steps(t *testing.T) {
	// Given
	client := &Client{
		protocol:     agentproto.AgentV2,
		interpreters: []executor.Interpreter{executor.InterpreterBash},
	}
	steps := []executor.Step{
		{Interpreter: executor.InterpreterBash},
		{Interpreter: executor.InterpreterPwsh},
		{
			Interpreter:    executor.InterpreterBash,
			ExecutionMode:  agentproto.ExecutionContainer,
			ContainerImage: "image",
		},
		{
			Interpreter:   executor.InterpreterBash,
			VariableNames: []string{"REGION"},
		},
	}
	for index, step := range steps {
		// When
		supported := client.SupportsStep(step)
		// Then
		if supported != (index == 0) {
			t.Fatalf("step %d support = %t", index, supported)
		}
	}
}

func TestClient_v3_rejected_by_legacy_server_without_claiming(t *testing.T) {
	// Given
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) },
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	client.protocol = agentproto.AgentV3
	// When
	claim, err := client.Poll(t.Context())
	// Then
	var status *StatusError
	if claim != nil || !errors.As(err, &status) ||
		status.Status != http.StatusBadRequest {
		t.Fatalf("claim = %v, error = %v", claim, err)
	}
}
