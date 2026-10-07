package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func claimLifecycleClient(
	t *testing.T,
	fixture *agentSubprocessFixture,
) (*agentclient.Client, string) {
	t.Helper()
	state, err := agentstate.New(fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverID.Fingerprint}, "agent-test")
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
	script := fmt.Sprintf(`#!/bin/bash
set -eu
fixture=%q
marker=%q
while [[ "$1" == --* ]]; do shift; done
case "$1" in
info) printf '%%s' '{"OSType":"linux","MemoryLimit":true,"CpuCfsQuota":true,"PidsLimit":true,"SecurityOptions":["name=seccomp,profile=builtin"]}' ;;
ps) if [[ -e "$fixture/owned" ]]; then printf 'owned\n'; fi ;;
image) printf '%%s' '[{"Id":"sha256:fixed","Config":{"Volumes":null}}]' ;;
run)
 touch "$fixture/ran" "$fixture/owned"
 printf 'ready\n'
 if [[ -e "$fixture/run-fail" ]]; then exit 7; fi ;;
rm)
 if [[ -e "$fixture/cleanup-fail" ]]; then exit 1; fi
 /usr/bin/rm -f "$fixture/owned"
 if [[ -e "$fixture/retire-fail" ]]; then
  /usr/bin/rm -f "$marker"
  mkdir "$marker"
  touch "$marker/block"
 fi ;;
*) exit 2 ;;
esac
`, directory, filepath.Join(fixture.stateDir, claimFileName))
	if err := os.WriteFile(
		filepath.Join(directory, "docker"),
		[]byte(script),
		0755,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+":"+os.Getenv("PATH"))
	t.Setenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY", "service")
	fixture.payload.Release.Steps[0].ExecutionMode = agentproto.ExecutionContainer
	fixture.payload.Release.Steps[0].ContainerImage = "test/image"
	client, err := agentclient.NewPaired(fixture.stateDir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	if err := client.EnableContainers(t.Context(), executor.ContainerConfig{
		Runtime: agentproto.RuntimeDocker, SocketURL: "unix://" + socket,
	}); err != nil {
		t.Fatal(err)
	}
	return client, directory
}
