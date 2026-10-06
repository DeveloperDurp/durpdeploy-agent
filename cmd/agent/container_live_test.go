//go:build containertest

package main

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestAgentContainerLive_executes_steps_through_mounted_socket(
	t *testing.T,
) {
	// Given
	runtime := os.Getenv("AGENT_TEST_RUNTIME")
	if runtime != "docker" && runtime != "podman" {
		t.Fatal("select AGENT_TEST_RUNTIME")
	}
	socket, err := url.Parse(os.Getenv("AGENT_TEST_SOCKET"))
	if err != nil || socket.Scheme != "unix" {
		t.Fatal("set AGENT_TEST_SOCKET")
	}
	fixture := newAgentSubprocessFixture(
		t,
		`printf 'container-agent\n'; printf '%s\n' "$SECRET"; test ! -e /run/durpdeploy/runtime.sock`,
	)
	fixture.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionContainer
	fixture.payload.Release.Steps[0].ContainerImage = "docker.io/library/bash:5.2"
	fixture.payload.Release.Steps[0].VariableNames = []string{"SECRET"}
	fixture.server.Close()
	fixture.serverID, err = agenttls.LoadOrCreate(
		t.TempDir(),
		"https://host.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	polls := make(chan agentproto.PollRequest, 2)
	server := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == agentproto.PollPath {
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				poll, err := agentproto.DecodeRequest[agentproto.PollRequest](
					bytes.NewReader(raw),
				)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				select {
				case polls <- poll:
				default:
				}
				r.Body = io.NopCloser(bytes.NewReader(raw))
			}
			fixture.handle(w, r)
		}),
	)
	if err := server.Listener.Close(); err != nil {
		t.Fatal(err)
	}
	server.Listener, err = net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{fixture.serverID.Certificate},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequestClientCert,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	state, err := agentstate.New(
		"https://host.test:"+port,
		[]agenttls.Fingerprint{fixture.serverID.Fingerprint},
		"agent-container-"+runtime,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(fixture.stateDir).Save(state); err != nil {
		t.Fatal(err)
	}
	image := os.Getenv("AGENT_TEST_IMAGE")
	if image == "" {
		t.Fatal("set AGENT_TEST_IMAGE")
	}
	name := fmt.Sprintf("durpdeploy-agent-contract-%s-%d", runtime, os.Getpid())
	clientArgs := []string{"--host=" + socket.String()}
	if runtime == "podman" {
		clientArgs = []string{"--remote", "--url=" + socket.String()}
	}
	args := []string{
		"run",
		"--name=" + name,
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=" + fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--tmpfs=/tmp:size=64m,mode=1777",
		"--memory=512m",
		"--cpus=1",
		"--pids-limit=128",
		"--volume=" + fixture.stateDir + ":/var/lib/durpdeploy-agent",
		"--volume=" + socket.Path + ":/run/durpdeploy/runtime.sock:ro",
		"--env=DURPDEPLOY_AGENT_CONTAINER_ENABLED=true",
		"--env=DURPDEPLOY_AGENT_CONTAINER_RUNTIME=" + runtime,
		"--env=DURPDEPLOY_AGENT_CONTAINER_SOCKET=unix:///run/durpdeploy/runtime.sock",
		"--env=DURPDEPLOY_AGENT_VERSION=container-contract",
		"--add-host=host.test:host-gateway",
	}
	if runtime == "podman" {
		args = append(
			args,
			"--userns=keep-id",
			"--network=slirp4netns:allow_host_loopback=true",
		)
	}
	info, err := os.Stat(socket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		args = append(args, fmt.Sprintf("--group-add=%d", stat.Gid))
	}
	args = append(args, image)
	command := exec.Command(runtime, append(clientArgs, args...)...)
	command.Stderr = &fixture.stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	exited := false
	defer func() {
		fixture.cancel()
		cleanup := exec.Command(
			runtime,
			append(clientArgs, "rm", "--force", "--volumes", name)...)
		if raw, err := cleanup.CombinedOutput(); err != nil {
			t.Errorf("remove test agent: %v: %s", err, raw)
		}
		if !exited {
			select {
			case <-done:
				exited = true
			case <-time.After(15 * time.Second):
				t.Error("agent client did not exit")
			}
		}
		if t.Failed() && exited {
			t.Logf("agent stderr: %s", fixture.stderr.String())
		}
	}()
	// When
	var result agentproto.ResultRequest
	select {
	case result = <-fixture.result:
	case err := <-done:
		exited = true
		t.Fatalf("agent exited: %v: %s", err, fixture.stderr.String())
	case <-time.After(5 * time.Minute):
		t.Fatal("agent did not return a result")
	}
	// Then
	if result.State != agentproto.ResultSucceeded {
		t.Fatalf("result: %+v", result)
	}
	poll := <-polls
	if poll.Protocol != agentproto.AgentV3 ||
		len(poll.ContainerRuntimes) != 1 ||
		string(poll.ContainerRuntimes[0]) != runtime {
		t.Fatalf("capabilities: %+v", poll)
	}
	output := strings.Join(fixture.logs(), "\n")
	if !strings.Contains(output, "container-agent") ||
		!strings.Contains(output, "[REDACTED]") ||
		strings.Contains(output, "subprocess-secret") {
		t.Fatalf("logs: %s", output)
	}
}
