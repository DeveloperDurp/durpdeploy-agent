//go:build containertest

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"slices"
	"strings"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// The workload waits in its private tmpfs until its uploaded log triggers runtime
// inspection. This checks requested quotas without assuming unlimited ancestors.
func verifyContainerResourceLogs(r *http.Request, runtime, socket string) error {
	if !strings.HasSuffix(r.URL.Path, "/logs") {
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
		config := containers[0].HostConfig
		if config.Memory != 0 || config.NanoCpus != 0 || config.CpuQuota != 0 || config.PidsLimit != 128 || !config.ReadonlyRootfs {
			return fmt.Errorf("public-protocol workload resources/isolation: %+v", config)
		}
		if err := exec.CommandContext(ctx, runtime, slices.Concat(clientArgs, []string{"exec", id, "touch", "/tmp/resources-checked"})...).Run(); err != nil {
			return fmt.Errorf("release inspected workload: %w", err)
		}
	}
	return nil
}
