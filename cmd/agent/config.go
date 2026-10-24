package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	"github.com/DeveloperDurp/durpdeploy-agent/configuration"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/buildinfo"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

type config struct {
	stateDir     string
	agentVersion agentproto.AgentVersion
	bootstrap    agentbootstrap.Config
	containers   bool
	container    executor.ContainerConfig
	execution    executor.StartupSettings
	limits       resourcepolicy.Limits
}

func loadConfig() (config, error) {
	settings, err := configuration.Load()
	if err != nil {
		return config{}, err
	}
	if _, err := executor.NewScrubberWithPatterns(nil, settings.ExtraScrubPatterns); err != nil {
		return config{}, err
	}
	stateDir, err := stateDirectory(settings.StateDir)
	if err != nil {
		return config{}, err
	}
	enabled := false
	if raw := settings.Container.Enabled; raw != "" {
		enabled, err = strconv.ParseBool(raw)
		if err != nil {
			return config{}, errors.New(
				"DURPDEPLOY_AGENT_CONTAINER_ENABLED must be true or false",
			)
		}
	}
	container := executor.ContainerConfig{}
	if enabled {
		container.Runtime, err = agentproto.ParseContainerRuntime(
			settings.Container.Runtime,
		)
		if err != nil {
			return config{}, err
		}
		container.SocketURL = settings.Container.Socket
		if container.SocketURL == "" {
			return config{}, fmt.Errorf(
				"DURPDEPLOY_AGENT_CONTAINER_SOCKET is required: %w",
				executor.ErrContainerUnavailable,
			)
		}
	}
	version := reportedAgentVersion(settings.Version)
	return config{
		containers:   enabled,
		container:    container,
		stateDir:     stateDir,
		agentVersion: version,
		limits:       settings.Limits,
		execution: executor.StartupSettings{
			ExecutionBoundary:  settings.ExecutionBoundary,
			Hostname:           settings.Hostname,
			ExtraScrubPatterns: settings.ExtraScrubPatterns,
		},
		bootstrap: agentbootstrap.Config{
			StateDir:     stateDir,
			AgentVersion: version,
			ListenAddr:   settings.ListenAddr,
		},
	}, nil
}

func reportedAgentVersion(override string) agentproto.AgentVersion {
	if override != "" {
		return agentproto.AgentVersion(override)
	}
	return agentproto.AgentVersion(buildinfo.Current().String())
}

func stateDirectory(directory string) (string, error) {
	if directory != "" {
		return directory, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "durpdeploy-agent"), nil
}
