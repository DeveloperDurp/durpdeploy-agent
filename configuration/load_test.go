package configuration_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/configuration"
)

func lookup(values map[string]string) configuration.Lookup {
	return func(key string) (string, bool) {
		value, set := values[key]
		return value, set
	}
}

func configFile(t *testing.T, content string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(file, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestDefaultsAndIndependentLoads(t *testing.T) {
	first, err := configuration.LoadWithLookup(lookup(nil))
	if err != nil || first != configuration.Defaults() {
		t.Fatalf("default load differs: %v", err)
	}
	first.Limits.Container.PIDs = 8
	second, err := configuration.LoadWithLookup(lookup(nil))
	if err != nil || second.Limits.Container.PIDs != 128 {
		t.Fatalf("loads share mutable state: %v", err)
	}
}

func TestCompleteYAMLAndEnvironment(t *testing.T) {
	file, err := os.ReadFile("testdata/all-settings.yaml")
	if err != nil {
		t.Fatal(err)
	}
	want, err := configuration.LoadWithLookup(lookup(map[string]string{
		configuration.FileEnv: configFile(t, string(file)),
	}))
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"DURPDEPLOY_AGENT_STATE_DIR":                                     "/private/state",
		"DURPDEPLOY_AGENT_LISTEN_ADDR":                                   "127.0.0.1:23188",
		"DURPDEPLOY_AGENT_VERSION":                                       "operator-build",
		"DURPDEPLOY_AGENT_HOSTNAME":                                      "demo-host",
		"DURPDEPLOY_AGENT_EXECUTION_BOUNDARY":                            "service",
		"DURPDEPLOY_EXTRA_SCRUB_PATTERNS":                                "PRIVATE_[A-Z]+",
		"DURPDEPLOY_AGENT_CONTAINER_ENABLED":                             "true",
		"DURPDEPLOY_AGENT_CONTAINER_RUNTIME":                             "podman",
		"DURPDEPLOY_AGENT_CONTAINER_SOCKET":                              "unix:///run/user/1000/podman/podman.sock",
		"DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES":                  "3",
		"DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS":          "1200",
		"DURPDEPLOY_AGENT_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS":      "30",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_DOWNLOAD_BYTES":                "1048576",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_EXTRACTED_BYTES":               "2097152",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_FILES":                         "20",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_STAGING_NODES":                 "39",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_METADATA_BYTES":                "65536",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_FETCH_TIMEOUT_SECONDS":         "120",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_DIAL_TIMEOUT_SECONDS":          "5",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_TLS_TIMEOUT_SECONDS":           "6",
		"DURPDEPLOY_AGENT_LIMITS_ARTIFACT_HEADER_TIMEOUT_SECONDS":        "7",
		"DURPDEPLOY_AGENT_LIMITS_STAGING_PAYLOAD_BYTES":                  "4096",
		"DURPDEPLOY_AGENT_LIMITS_STAGING_ENTRIES":                        "10",
		"DURPDEPLOY_AGENT_LIMITS_STAGING_ARCHIVE_BYTES":                  "12288",
		"DURPDEPLOY_AGENT_LIMITS_TRANSFER_IDLE_TIMEOUT_SECONDS":          "45",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS":                         "16",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_TMPFS_BYTES":                  "4096",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_PIDS":                  "4",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_CPUS":                  "0.5",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_MEMORY_OVERHEAD_BYTES": "8192",
	}
	got, err := configuration.LoadWithLookup(lookup(env))
	if err != nil || got != want {
		t.Fatalf("environment and YAML differ: got=%+v want=%+v err=%v", got, want, err)
	}
	// Every fixture field must be present and distinct from its default.
	assertDistinctFields(t, reflect.ValueOf(got), reflect.ValueOf(configuration.Defaults()))
}

func assertDistinctFields(t *testing.T, value, defaults reflect.Value) {
	t.Helper()
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).Kind() == reflect.Struct {
			assertDistinctFields(t, value.Field(i), defaults.Field(i))
		} else if value.Field(i).Interface() == defaults.Field(i).Interface() {
			t.Fatalf("fixture misses nondefault %s", value.Type().Field(i).Name)
		}
	}
}

func TestPrecedenceAndExplicitEmpty(t *testing.T) {
	file := configFile(t, "hostname: yaml-host\ncontainer:\n  enabled: true\nlimits:\n  container:\n    pids: 0\n")
	settings, err := configuration.LoadWithLookup(lookup(map[string]string{
		configuration.FileEnv:                    file,
		"DURPDEPLOY_AGENT_HOSTNAME":              "",
		"DURPDEPLOY_AGENT_CONTAINER_ENABLED":     "",
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": "8",
	}))
	if err != nil || settings.Hostname != "" || settings.Container.Enabled != "" ||
		settings.Limits.Container.PIDs != 8 || settings.Limits.Container.HelperPIDs != 32 {
		t.Fatalf("incorrect merge: %+v, %v", settings, err)
	}
	_, err = configuration.LoadWithLookup(lookup(map[string]string{
		configuration.FileEnv:                    file,
		"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": "",
	}))
	if err == nil {
		t.Fatal("empty numeric override restored YAML")
	}
}

func TestStrictYAMLAndRedactedErrors(t *testing.T) {
	const secret = "input-must-not-leak"
	cases := []string{
		"", "[]", "null", "hostname: a\nhostname: b\n",
		secret + ": value\n", "container:\n  " + secret + ": value\n",
		"hostname: [" + secret + "]\n", "hostname: null\n",
		"hostname: &anchor " + secret + "\nversion: *anchor\n",
		"&anchor hostname: " + secret + "\n",
		"container: &anchor {}\n", "hostname: !custom " + secret + "\n",
		"limits:\n  container:\n    pids: '8'\n",
		"limits:\n  container:\n    pids: 9223372036854775808\n",
		"limits:\n  container:\n    pids: 8.5\n",
		"limits:\n  container:\n    helper_cpus: .nan\n",
		"hostname: a\n---\nhostname: b\n",
		"hostname: [" + secret,
	}
	for _, content := range cases {
		t.Run(content, func(t *testing.T) {
			_, err := configuration.LoadWithLookup(lookup(map[string]string{
				configuration.FileEnv: configFile(t, content),
			}))
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("expected a redacted error, got %v", err)
			}
		})
	}
}

func TestMissingFileAndEnvironmentErrors(t *testing.T) {
	const secret = "input-must-not-leak"
	for name, env := range map[string]map[string]string{
		"missing file":     {configuration.FileEnv: filepath.Join(t.TempDir(), secret)},
		"integer syntax":   {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": secret},
		"integer overflow": {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS": "9223372036854775808"},
		"float syntax":     {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_CPUS": secret},
		"float infinity":   {"DURPDEPLOY_AGENT_LIMITS_CONTAINER_HELPER_CPUS": "+Inf"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := configuration.LoadWithLookup(lookup(env))
			if err == nil || strings.Contains(err.Error(), secret) {
				t.Fatalf("expected a redacted error, got %v", err)
			}
		})
	}
}
