//go:build containertest

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
)

// The workload waits in its private tmpfs until its uploaded log triggers runtime
// inspection. This checks requested quotas without assuming unlimited ancestors.
func (fixture *agentSubprocessFixture) verifyContainerResourceLogs(r *http.Request, runtime, socket string) error {
	if r.Method != http.MethodPost || r.URL.Path != "/agent/v1/deployments/42/logs" {
		return nil
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("read resource-check logs: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	batch, err := agentproto.DecodeLogBatch(bytes.NewReader(raw))
	if err != nil {
		return err
	}
	clientArgs := []string{"--host=" + socket}
	if runtime == "podman" {
		clientArgs = []string{"--remote", "--url=" + socket}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, event := range batch.Events {
		id, check := strings.CutPrefix(event.Line, "inspect-resources:")
		if !check {
			continue
		}
		raw, err := exec.CommandContext(ctx, runtime, slices.Concat(clientArgs, []string{"inspect", id})...).Output()
		if err != nil {
			return fmt.Errorf("inspect public-protocol workload: %w", err)
		}
		var containers []struct {
			ID, Name   string
			Config     struct{ Labels map[string]string }
			HostConfig struct {
				Memory, NanoCpus, CpuQuota, PidsLimit int64
				ReadonlyRootfs                        bool
			}
		}
		if err := json.Unmarshal(raw, &containers); err != nil {
			return err
		}
		if len(containers) != 1 {
			return fmt.Errorf("expected one public-protocol workload, got %d", len(containers))
		}
		state, err := agentstate.NewStore(fixture.stateDir).Load()
		if err != nil {
			return err
		}
		identity, err := json.Marshal([]string{state.AgentID, state.ServerURL + "\x00" + fixture.identity.Fingerprint.String()})
		if err != nil {
			return err
		}
		namespace := fmt.Sprintf("agent-%x", sha256.Sum256(identity))
		labels := containers[0].Config.Labels
		if labels["io.durpdeploy.agent"] != namespace || labels["io.durpdeploy.attempt"] != strings.TrimPrefix(containers[0].Name, "/") {
			return fmt.Errorf("resource-check container is outside this test's attempt namespace")
		}
		config := containers[0].HostConfig
		if config.Memory != 0 || config.NanoCpus != 0 || config.CpuQuota != 0 || config.PidsLimit != 128 || !config.ReadonlyRootfs {
			return fmt.Errorf("public-protocol workload resources/isolation: %+v", config)
		}
		if err := exec.CommandContext(ctx, runtime, slices.Concat(clientArgs, []string{"exec", containers[0].ID, "touch", "/tmp/resources-checked"})...).Run(); err != nil {
			return fmt.Errorf("release inspected workload: %w", err)
		}
	}
	return nil
}
