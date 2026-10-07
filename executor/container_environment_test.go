package executor

import (
	"errors"
	"maps"
	"testing"
)

func TestStepEnvironment_default_container_filters_runtime_settings(
	t *testing.T,
) {
	// Given
	env := map[string]string{
		"REGION":        "west",
		"DEPLOY_SECRET": "private\nvalue",
		"NAME_2":        "value",
	}
	for _, name := range []string{"DOCKER_HOST", "PODMAN_CONNECTIONS_CONF", "CONTAINER_HOST", "CONTAINERS_CONF", "SSH_AUTH_SOCK", "XDG_CONFIG_HOME", "HOME", "PATH", "TERM", "TMPDIR", "REGISTRY_AUTH_FILE", "LD_PRELOAD", "LD_LIBRARY_PATH", "GODEBUG", "GOTRACEBACK", "GOMEMLIMIT", "GOMAXPROCS", "BASH_ENV", "ENV"} {
		env[name] = "untrusted"
	}
	// When
	selected, err := selectStepEnvironment(env, nil, true)
	// Then
	if err != nil ||
		!maps.Equal(
			selected,
			map[string]string{
				"REGION":        "west",
				"DEPLOY_SECRET": "private\nvalue",
				"NAME_2":        "value",
			},
		) {
		t.Fatalf("container environment: %v, %v", selected, err)
	}
	host, err := selectStepEnvironment(env, nil, false)
	if err != nil || !maps.Equal(host, env) {
		t.Fatalf("host environment changed: %v, %v", host, err)
	}
}

func TestStepEnvironment_rejects_invalid_or_unavailable_selected_variables(
	t *testing.T,
) {
	for _, scenario := range []struct {
		name, value string
		selected    bool
	}{
		{"", "value", false},
		{"2NAME", "value", false},
		{"BAD-NAME", "value", false},
		{"NAME", "bad\x00value", false},
		{"HOME", "untrusted", true},
		{"LD_PRELOAD", "untrusted", true},
		{"GODEBUG", "untrusted", true},
		{"BAD-NAME", "value", true},
		{"NAME", "bad\x00value", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Given
			var names []string
			if scenario.selected {
				names = []string{scenario.name}
			}
			// When
			_, err := selectStepEnvironment(
				map[string]string{scenario.name: scenario.value},
				names,
				true,
			)
			// Then
			if !errors.Is(err, ErrInvalidStepExecution) {
				t.Fatalf("invalid variable accepted: %v", err)
			}
		})
	}
	if _, err := selectStepEnvironment(
		nil,
		[]string{"MISSING"},
		true,
	); !errors.Is(
		err,
		ErrInvalidStepExecution,
	) {
		t.Fatalf("missing variable accepted: %v", err)
	}
}
