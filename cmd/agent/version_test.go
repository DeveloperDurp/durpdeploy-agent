package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAgentVersionSubprocess(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "durpdeploy-agent")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, output)
	}
	identify := exec.Command(binary, "version")
	identify.Env = []string{}
	identity, err := identify.Output()
	if err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	lease, err := acquireStateLease(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	before, err := os.ReadDir(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, argument := range []string{"version", "--version", "-v"} {
		for _, override := range []string{"", "operator-override"} {
			t.Run(argument+"/"+override, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, binary, argument)
				command.Env = []string{
					"DURPDEPLOY_AGENT_STATE_DIR=" + stateDir,
					"DURPDEPLOY_AGENT_CONTAINER_ENABLED=invalid",
					"DURPDEPLOY_AGENT_LISTEN_ADDR=invalid",
					"DURPDEPLOY_AGENT_VERSION=" + override,
				}
				output, err := command.CombinedOutput()
				want := string(identity)
				if override != "" {
					want += "Reported agent version override: \"operator-override\"\n"
				}
				if err != nil || string(output) != want {
					t.Fatalf("version output=%q error=%v want=%q", output, err, want)
				}
			})
		}
	}
	after, err := os.ReadDir(stateDir)
	if err != nil || len(before) != len(after) {
		t.Fatalf("version commands changed state directory: %v", err)
	}
}
