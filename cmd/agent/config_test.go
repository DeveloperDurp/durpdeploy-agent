package main

import (
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/buildinfo"
)

func TestLoadConfig_reportedVersion(t *testing.T) {
	for _, override := range []string{"", "operator-version"} {
		t.Run(override, func(t *testing.T) {
			t.Setenv("DURPDEPLOY_AGENT_VERSION", override)
			t.Setenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED", "false")
			configuration, err := loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			want := override
			if want == "" {
				want = buildinfo.Current().String()
			}
			if string(configuration.agentVersion) != want ||
				string(configuration.bootstrap.AgentVersion) != want {
				t.Fatalf("pairing/poll version=%q/%q want=%q",
					configuration.bootstrap.AgentVersion, configuration.agentVersion, want)
			}
		})
	}
}

func TestAgentHelp_listsOnlyLocalBootstrapInputs(t *testing.T) {
	// Given
	const forbidden = "DURPDEPLOY_AGENT_SERVER_URL"

	// When
	help := agentHelp()

	// Then
	for _, required := range []string{
		"DURPDEPLOY_AGENT_LISTEN_ADDR",
		"DURPDEPLOY_AGENT_STATE_DIR",
		"DURPDEPLOY_AGENT_VERSION",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("help does not mention %s: %s", required, help)
		}
	}
	if strings.Contains(help, forbidden) {
		t.Fatalf("help exposes manual server configuration: %s", help)
	}
}

func TestLoadConfig_usesDefaultStateDirectory(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_STATE_DIR", "")

	// When
	configuration, err := loadConfig()

	// Then
	if err != nil || configuration.stateDir == "" {
		t.Fatalf("default config = %#v, %v", configuration, err)
	}
}

func TestLoadConfig_ignoresManualServerConfiguration(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_SERVER_URL", "https://server.example.test")
	t.Setenv("DURPDEPLOY_AGENT_SERVER_FINGERPRINT", strings.Repeat("a", 64))
	t.Setenv("DURPDEPLOY_AGENT_ID", "manual-agent")
	t.Setenv("DURPDEPLOY_AGENT_NAME", "manual-agent")

	// When
	configuration, err := loadConfig()

	// Then
	if err != nil || configuration.stateDir == "" {
		t.Fatalf(
			"manual server configuration was retained: %#v, %v",
			configuration,
			err,
		)
	}
}

func TestLoadConfig_container_execution_requires_explicit_configuration(
	t *testing.T,
) {
	for _, scenario := range []string{"disabled", "invalid-enabled", "missing-runtime", "missing-socket", "valid"} {
		t.Run(scenario, func(t *testing.T) {
			// Given
			t.Setenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED", "true")
			t.Setenv("DURPDEPLOY_AGENT_CONTAINER_RUNTIME", "podman")
			t.Setenv(
				"DURPDEPLOY_AGENT_CONTAINER_SOCKET",
				"unix:///run/podman/podman.sock",
			)
			switch scenario {
			case "disabled":
				t.Setenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED", "")
			case "invalid-enabled":
				t.Setenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED", "sometimes")
			case "missing-runtime":
				t.Setenv("DURPDEPLOY_AGENT_CONTAINER_RUNTIME", "")
			case "missing-socket":
				t.Setenv("DURPDEPLOY_AGENT_CONTAINER_SOCKET", "")
			}
			// When
			configuration, err := loadConfig()
			// Then
			if scenario == "valid" {
				if err != nil || !configuration.containers {
					t.Fatalf("enabled config: %v", err)
				}
				return
			}
			if scenario == "disabled" {
				if err != nil || configuration.containers {
					t.Fatalf("default config: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("accepted incomplete container config")
			}
		})
	}
}
