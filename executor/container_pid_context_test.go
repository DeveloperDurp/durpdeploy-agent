package executor

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainer_PID_probe_timeout_preserves_retries(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given: the runtime info command stalls after startup.
			runner, directory := containerFixture(t, runtime)
			if err := os.WriteFile(filepath.Join(directory, "info-wait"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			job := NewJob(JobConfig{
				ScriptBody: "exit 0", ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "test/image", Timeout: 100 * time.Millisecond, MaxRetries: 1,
			})
			// When
			err := NewExecutorWithContainers(runner).Execute(t.Context(), job, Callbacks{})
			// Then: the step deadline remains identifiable and each retry probes again.
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("probe timeout: %v", err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(actions), "info\n") != 2 || strings.Contains(string(actions), "run\n") {
				t.Fatalf("timed out probe attempts: %s", actions)
			}
		})
	}
}

func TestContainer_PID_probe_preserves_parent_cancellation(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given: a stalled probe signals when it starts through a local socket.
			runner, directory := containerFixture(t, runtime)
			listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			if err := listener.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			host, port, err := net.SplitHostPort(listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "info-notify"), []byte(host+" "+port+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(directory, "info-wait"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			job := NewJob(JobConfig{
				ScriptBody: "exit 0", ExecutionMode: agentproto.ExecutionContainer,
				ContainerImage: "test/image", MaxRetries: 1,
			})
			// When: cancellation arrives while the PID probe is running.
			result := make(chan error, 1)
			go func() {
				result <- NewExecutorWithContainers(runner).Execute(ctx, job, Callbacks{})
			}()
			connection, err := listener.Accept()
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if err := connection.Close(); err != nil {
				t.Fatal(err)
			}
			// Then
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled probe: %v", err)
			}
		})
	}
}
