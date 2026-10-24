package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/configuration"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"go.yaml.in/yaml/v3"
)

func isolateStartupEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "DURPDEPLOY_AGENT_") || key == "DURPDEPLOY_EXTRA_SCRUB_PATTERNS" {
			t.Setenv(key, "")
			if err := os.Unsetenv(key); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func startupConfigFile(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestLoadConfigCapturesAllYAMLStartupInputs(t *testing.T) {
	isolateStartupEnvironment(t)
	stateDir := filepath.Join(t.TempDir(), "state-not-created")
	file := startupConfigFile(t, fmt.Sprintf(`state_dir: %q
listen_addr: 127.0.0.1:23188
version: yaml-build
hostname: yaml-host
execution_boundary: service
extra_scrub_patterns: CUSTOM_[0-9]+
container:
  enabled: true
  runtime: podman
  socket: unix:///run/user/1000/podman/podman.sock
`, stateDir))
	t.Setenv(configuration.FileEnv, file)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.stateDir != stateDir || cfg.bootstrap.StateDir != stateDir || cfg.bootstrap.ListenAddr != "127.0.0.1:23188" ||
		cfg.agentVersion != "yaml-build" || cfg.bootstrap.AgentVersion != "yaml-build" {
		t.Fatalf("incorrect startup inputs: %+v", cfg)
	}
	if !cfg.containers || cfg.container.Runtime != "podman" || cfg.container.SocketURL != "unix:///run/user/1000/podman/podman.sock" {
		t.Fatalf("incorrect container inputs: %+v", cfg.container)
	}
	if cfg.execution != (executor.StartupSettings{ExecutionBoundary: "service", Hostname: "yaml-host", ExtraScrubPatterns: "CUSTOM_[0-9]+"}) {
		t.Fatalf("incorrect captured execution settings: %+v", cfg.execution)
	}
	if cfg.limits != resourcepolicy.Defaults() {
		t.Fatalf("incorrect captured resource policy: %+v", cfg.limits)
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("loading created state: %v", err)
	}
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "changed-after-load")
	if cfg.execution.Hostname != "yaml-host" {
		t.Fatal("captured hostname changed")
	}
}

func TestLoadConfigEnvironmentAndExplicitEmptyOverrides(t *testing.T) {
	isolateStartupEnvironment(t)
	t.Setenv(configuration.FileEnv, startupConfigFile(t, "hostname: yaml-host\nversion: yaml-version\ncontainer:\n  enabled: true\n"))
	t.Setenv("DURPDEPLOY_AGENT_CONTAINER_ENABLED", "")
	t.Setenv("DURPDEPLOY_AGENT_HOSTNAME", "")
	t.Setenv("DURPDEPLOY_AGENT_VERSION", "env-version")
	cfg, err := loadConfig()
	if err != nil || cfg.containers || cfg.execution.Hostname != "" || cfg.agentVersion != "env-version" {
		t.Fatalf("incorrect empty overrides: %+v, %v", cfg, err)
	}
}

func TestLoadConfigRejectsInvalidResourcesAndRedactsInputs(t *testing.T) {
	for name, overrides := range map[string]map[string]string{
		"workload PID":          {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": "0"},
		"helper PID":            {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_PIDS": "-1"},
		"retry policy":          {"DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES": "-1"},
		"empty numeric":         {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": ""},
		"nonfinite CPU":         {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_CPUS": "NaN"},
		"duration overflow":     {"DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS": "9223372036854775807"},
		"invalid Boolean":       {"DURPDEPLOY_AGENT_CONTAINER_ENABLED": "private-input"},
		"invalid scrub pattern": {"DURPDEPLOY_EXTRA_SCRUB_PATTERNS": "[private-input"},
	} {
		t.Run(name, func(t *testing.T) {
			isolateStartupEnvironment(t)
			stateDir := filepath.Join(t.TempDir(), "state-not-created")
			t.Setenv("DURPDEPLOY_AGENT_STATE_DIR", stateDir)
			for key, value := range overrides {
				t.Setenv(key, value)
			}
			_, err := loadConfig()
			if err == nil || strings.Contains(err.Error(), "private-input") {
				t.Fatalf("expected redacted rejection: %v", err)
			}
			if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
				t.Fatalf("invalid startup created state: %v", err)
			}
		})
	}
}

func TestLoadConfigAcceptsEveryResourceField(t *testing.T) {
	value := reflect.ValueOf(resourcepolicy.Defaults())
	for group := 0; group < value.NumField(); group++ {
		fields := value.Field(group)
		for field := 0; field < fields.NumField(); field++ {
			name := value.Type().Field(group).Name + "/" + fields.Type().Field(field).Name
			t.Run(name, func(t *testing.T) { checkStartupResourceField(t, group, field) })
		}
	}
}

func checkStartupResourceField(t *testing.T, group, field int) {
	t.Helper()
	isolateStartupEnvironment(t)
	stateDir := filepath.Join(t.TempDir(), "state-not-created")
	t.Setenv("DURPDEPLOY_AGENT_STATE_DIR", stateDir)
	want := resourcepolicy.Defaults()
	values := reflect.ValueOf(&want).Elem()
	section := values.Field(group)
	setting := section.Field(field)
	var raw string
	if setting.Kind() == reflect.Int64 {
		setting.SetInt(setting.Int() + 1)
		raw = strconv.FormatInt(setting.Int(), 10)
	} else {
		setting.SetFloat(setting.Float() + .5)
		raw = strconv.FormatFloat(setting.Float(), 'g', -1, 64)
	}
	key := "DURPDEPLOY_AGENT_LIMITS_" + strings.ToUpper(values.Type().Field(group).Tag.Get("yaml")+"_"+section.Type().Field(field).Tag.Get("yaml"))
	t.Setenv(key, raw)
	cfg, err := loadConfig()
	if err != nil || cfg.limits != want {
		t.Fatalf("configured resource not captured: %v", err)
	}
	t.Setenv(key, "0")
	if cfg.limits != want {
		t.Fatal("startup snapshot changed after environment mutation")
	}
	if _, err := os.Stat(stateDir); !os.IsNotExist(err) {
		t.Fatalf("loading configuration created state: %v", err)
	}
}

func TestLoadConfigCustomYAMLAndEnvironmentPolicy(t *testing.T) {
	isolateStartupEnvironment(t)
	settings := configuration.Defaults()
	settings.StateDir = filepath.Join(t.TempDir(), "state-not-created")
	settings.Limits.Execution.MaxRetries = 12
	settings.Limits.Container.PIDs = 160
	settings.Limits.Staging.PayloadBytes = 1 << 20
	settings.Limits.Staging.ArchiveBytes = 2 << 20
	settings.Limits.Staging.Entries = 32
	raw, err := yaml.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(configuration.FileEnv, startupConfigFile(t, string(raw)))
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES", "0")
	t.Setenv("DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS", "24")
	want := settings.Limits
	want.Execution.MaxRetries, want.Container.PIDs = 0, 24
	cfg, err := loadConfig()
	if err != nil || cfg.limits != want {
		t.Fatalf("mixed policy not captured: %v", err)
	}
	if _, err := os.Stat(settings.StateDir); !os.IsNotExist(err) {
		t.Fatalf("loading created state: %v", err)
	}
}
