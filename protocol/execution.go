package agentproto

import (
	"encoding/json"
	"slices"
)

type ExecutionMode string

const (
	ExecutionHost      ExecutionMode = "host"
	ExecutionContainer ExecutionMode = "container"
)

func ParseExecutionMode(raw string) (ExecutionMode, error) {
	switch mode := ExecutionMode(raw); mode {
	case ExecutionHost, ExecutionContainer:
		return mode, nil
	default:
		return "", protocolError(
			"execution_mode",
			ReasonInvalid,
			ErrInvalidCapability,
		)
	}
}

func (m *ExecutionMode) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("execution_mode", ReasonInvalid, ErrInvalidJSON)
	}
	parsed, err := ParseExecutionMode(raw)
	if err == nil {
		*m = parsed
	}
	return err
}

type ContainerRuntime string

const (
	RuntimeDocker ContainerRuntime = "docker"
	RuntimePodman ContainerRuntime = "podman"
)

func ParseContainerRuntime(raw string) (ContainerRuntime, error) {
	switch runtime := ContainerRuntime(raw); runtime {
	case RuntimeDocker, RuntimePodman:
		return runtime, nil
	default:
		return "", protocolError(
			"container_runtimes",
			ReasonInvalid,
			ErrInvalidCapability,
		)
	}
}

func (r *ContainerRuntime) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError(
			"container_runtimes",
			ReasonInvalid,
			ErrInvalidJSON,
		)
	}
	parsed, err := ParseContainerRuntime(raw)
	if err == nil {
		*r = parsed
	}
	return err
}

// ExecutionCapabilities distinguishes local executables from fixed container
// entrypoints. Container support does not prove an interpreter exists in an image.
type ExecutionCapabilities struct {
	ExecutionModes        []ExecutionMode    `json:"execution_modes"`
	ContainerRuntimes     []ContainerRuntime `json:"container_runtimes"`
	ContainerInterpreters []Interpreter      `json:"container_interpreters"`
}

func (c ExecutionCapabilities) validate(host []Interpreter) error {
	if len(c.ExecutionModes) == 0 {
		return protocolError(
			"execution_modes",
			ReasonInvalid,
			ErrInvalidCapability,
		)
	}
	if err := uniqueCapabilities(
		"execution_modes",
		c.ExecutionModes,
	); err != nil {
		return err
	}
	if err := uniqueCapabilities(
		"container_runtimes",
		c.ContainerRuntimes,
	); err != nil {
		return err
	}
	if err := uniqueCapabilities(
		"container_interpreters",
		c.ContainerInterpreters,
	); err != nil {
		return err
	}
	hasHost := slices.Contains(c.ExecutionModes, ExecutionHost)
	hasContainer := slices.Contains(c.ExecutionModes, ExecutionContainer)
	if hasHost != (len(host) > 0) ||
		hasContainer != (len(c.ContainerRuntimes) > 0) ||
		hasContainer != (len(c.ContainerInterpreters) > 0) {
		return protocolError(
			"capabilities",
			ReasonInvalid,
			ErrInvalidCapability,
		)
	}
	return nil
}

func uniqueCapabilities[T comparable](field string, values []T) error {
	seen := make(map[T]bool, len(values))
	for _, value := range values {
		if seen[value] {
			return protocolError(field, ReasonDuplicate, ErrInvalidCapability)
		}
		seen[value] = true
	}
	return nil
}
