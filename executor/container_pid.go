package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// requirePIDLimit checks each attempt because some runtimes silently ignore
// --pids-limit without a usable controller. This does not gate startup or polling.
func (r *ContainerExecutor) requirePIDLimit(ctx context.Context) error {
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	raw, err := r.command(checkCtx, "info", "--format", "{{json .}}").Output()
	if err != nil {
		if checkCtx.Err() != nil {
			return fmt.Errorf("check runtime PID limit support: %w", checkCtx.Err())
		}
		return fmt.Errorf("check runtime PID limit support: %w", ErrContainerUnavailable)
	}
	var info struct {
		PidsLimit    bool
		ServerErrors []string
		Host         struct {
			CgroupControllers []string
		}
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf("decode runtime PID limit support: %w", ErrContainerUnavailable)
	}
	if len(info.ServerErrors) > 0 {
		return fmt.Errorf("check runtime socket access: %w", ErrContainerUnavailable)
	}
	var supported bool
	switch r.runtime {
	case agentproto.RuntimeDocker:
		supported = info.PidsLimit
	case agentproto.RuntimePodman:
		supported = slices.Contains(info.Host.CgroupControllers, "pids")
	}
	if !supported {
		return fmt.Errorf("container steps require PID limit support: %w", ErrContainerUnavailable)
	}
	return nil
}
