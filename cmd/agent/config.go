package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/buildinfo"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type config struct {
	stateDir     string
	agentVersion agentproto.AgentVersion
	bootstrap    agentbootstrap.Config
	containers   bool
	container    executor.ContainerConfig
}

func loadConfig() (config, error) {
	stateDir, err := stateDirectory()
	if err != nil {
		return config{}, err
	}
	enabled := false
	if raw := os.Getenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED"); raw != "" {
		enabled, err = strconv.ParseBool(raw)
		if err != nil {
			return config{}, fmt.Errorf(
				"DURPDEPLOY_AGENT_CONTAINER_ENABLED must be true or false: %w",
				err,
			)
		}
	}
	container := executor.ContainerConfig{}
	if enabled {
		container.Runtime, err = agentproto.ParseContainerRuntime(
			os.Getenv("DURPDEPLOY_AGENT_CONTAINER_RUNTIME"),
		)
		if err != nil {
			return config{}, err
		}
		container.SocketURL = os.Getenv("DURPDEPLOY_AGENT_CONTAINER_SOCKET")
		if container.SocketURL == "" {
			return config{}, fmt.Errorf(
				"DURPDEPLOY_AGENT_CONTAINER_SOCKET is required: %w",
				executor.ErrContainerUnavailable,
			)
		}
	}
	return config{
		containers:   enabled,
		container:    container,
		stateDir:     stateDir,
		agentVersion: reportedAgentVersion(),
		bootstrap: agentbootstrap.Config{
			StateDir:     stateDir,
			AgentVersion: reportedAgentVersion(),
			ListenAddr: os.Getenv(
				"DURPDEPLOY_AGENT_LISTEN_ADDR",
			),
		},
	}, nil
}

func reportedAgentVersion() agentproto.AgentVersion {
	if override := os.Getenv("DURPDEPLOY_AGENT_VERSION"); override != "" {
		return agentproto.AgentVersion(override)
	}
	return agentproto.AgentVersion(buildinfo.Current().String())
}

func stateDirectory() (string, error) {
	if directory := os.Getenv("DURPDEPLOY_AGENT_STATE_DIR"); directory != "" {
		return directory, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "durpdeploy-agent"), nil
}
