//go:build containertest

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainerLive_attempts_preserve_isolation_without_resource_quotas(t *testing.T) {
	for _, scenario := range []string{"success", "retries"} {
		t.Run(scenario, func(t *testing.T) {
			// Given
			runner := liveContainerRunner(t)
			attempts, exitCode := 1, 0
			if scenario == "retries" {
				attempts, exitCode = 3, 127
			}
			job := NewJob(JobConfig{
				ScriptBody:     fmt.Sprintf(`echo inspect-resources; while [ ! -e /tmp/resources-checked ]; do sleep 0.1; done; exit %d`, exitCode),
				ExecutionMode:  agentproto.ExecutionContainer,
				ContainerImage: "docker.io/library/bash:5.2", MaxRetries: 2, Timeout: time.Minute,
			})
			inspected := 0
			// When
			err := NewExecutorWithContainers(runner).Execute(t.Context(), job, NewCallbacks(CallbacksConfig{
				WriteLog: func(line string) error {
					if line != "inspect-resources" {
						return nil
					}
					ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
					defer cancel()
					ids, err := runner.ownedContainers(ctx)
					if err != nil {
						return err
					}
					if len(ids) != 1 {
						return fmt.Errorf("expected one live attempt, got %v", ids)
					}
					raw, err := runner.command(ctx, "inspect", ids[0]).Output()
					if err != nil {
						return err
					}
					var containers []struct {
						Config     struct{ User string }
						HostConfig struct {
							Memory, NanoCpus, CpuQuota, PidsLimit int64
							ReadonlyRootfs                        bool
							NetworkMode                           string
							Tmpfs                                 map[string]string
						}
					}
					if err := json.Unmarshal(raw, &containers); err != nil {
						return err
					}
					if len(containers) != 1 {
						return fmt.Errorf("expected one inspected container, got %d", len(containers))
					}
					config := containers[0].HostConfig
					if config.Memory != 0 || config.NanoCpus != 0 || config.CpuQuota != 0 {
						return fmt.Errorf("step resource quotas: %+v", config)
					}
					if config.PidsLimit != 128 || !config.ReadonlyRootfs || config.NetworkMode != "none" || containers[0].Config.User != "65534:65534" || !strings.Contains(config.Tmpfs["/tmp"], "size=64m") {
						return fmt.Errorf("step isolation: %+v", containers[0])
					}
					inspected++
					return runner.command(ctx, "exec", ids[0], "touch", "/tmp/resources-checked").Run()
				},
			}))
			// Then
			var exitErr *exec.ExitError
			if exitCode == 0 && err != nil {
				t.Fatal(err)
			}
			if exitCode != 0 && (!errors.As(err, &exitErr) || exitErr.ExitCode() != exitCode) {
				t.Fatalf("retry failure: %v", err)
			}
			if inspected != attempts {
				t.Fatalf("inspected %d attempts, want %d", inspected, attempts)
			}
			ids, err := runner.ownedContainers(t.Context())
			if err != nil || len(ids) != 0 {
				t.Fatalf("containers remain: %v, %v", ids, err)
			}
		})
	}
}
