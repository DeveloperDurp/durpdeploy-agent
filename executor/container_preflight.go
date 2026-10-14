package executor

import (
	"encoding/json"
	"fmt"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *ContainerExecutor) validateRuntimeInfo(raw []byte) error {
	var info struct {
		Name         string
		ServerErrors []string
		Host         struct {
			Hostname string
		}
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf(
			"decode runtime preflight: %w",
			ErrContainerUnavailable,
		)
	}
	if len(info.ServerErrors) > 0 {
		return fmt.Errorf(
			"check runtime socket access: %w",
			ErrContainerUnavailable,
		)
	}
	r.hostname = info.Name
	if r.runtime == agentproto.RuntimePodman {
		r.hostname = info.Host.Hostname
	}
	return nil
}
