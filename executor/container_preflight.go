package executor

import (
	"encoding/json"
	"fmt"
	"slices"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *ContainerExecutor) validateRuntimeInfo(raw []byte) error {
	var info struct {
		Name        string
		OSType      string
		MemoryLimit bool
		CpuCfsQuota bool
		PidsLimit   bool
		Host        struct {
			Hostname          string
			OS                string
			CgroupControllers []string
			Security          struct {
				SeccompEnabled     bool
				SeccompProfilePath string
			}
		}
		SecurityOptions []string
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf(
			"decode runtime preflight: %w",
			ErrContainerUnavailable,
		)
	}
	ready := info.OSType == "linux" && info.MemoryLimit && info.CpuCfsQuota &&
		info.PidsLimit
	seccomp := slices.Contains(
		info.SecurityOptions,
		"name=seccomp,profile=builtin",
	)
	if r.runtime == agentproto.RuntimePodman {
		ready = info.Host.OS == "linux"
		for _, controller := range []string{"cpu", "memory", "pids"} {
			ready = ready &&
				slices.Contains(info.Host.CgroupControllers, controller)
		}
		seccomp = info.Host.Security.SeccompEnabled &&
			info.Host.Security.SeccompProfilePath == podmanSeccompProfile
	}
	if !ready || !seccomp {
		return fmt.Errorf(
			"runtime requires Linux, seccomp and CPU/memory/PID cgroup limits: %w",
			ErrContainerUnavailable,
		)
	}
	r.hostname = info.Name
	if r.runtime == agentproto.RuntimePodman {
		r.hostname = info.Host.Hostname
	}
	return nil
}
