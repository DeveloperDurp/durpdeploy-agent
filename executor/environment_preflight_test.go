//go:build linux

package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutorRejectsInvalidHostEnvironmentBeforeStarting(t *testing.T) {
	for _, selected := range []bool{false, true} {
		for _, scenario := range []struct{ name, value string }{
			{"", "value"},
			{"BAD\x00NAME", "value"},
			{"BAD=NAME", "value"},
			{"bad name", "value"},
			{"BAD\nNAME", "value"},
			{"2NAME", "value"},
			{"NÁME", "value"},
			{strings.Repeat("A", 256), "value"},
			{"VALID", "private\x00value"},
		} {
			label := scenario.name + "/unrestricted"
			var names []string
			if selected {
				names = []string{scenario.name}
				label = scenario.name + "/selected"
			}
			t.Run(label, func(t *testing.T) {
				assertHostEnvironmentRejected(t, scenario.name, scenario.value, names)
			})
		}
	}
}

func assertHostEnvironmentRejected(t *testing.T, name, value string, names []string) {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "started")
	started := false
	var logs strings.Builder
	err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
		Name: "invalid environment", ScriptBody: "touch " + marker,
		VariableNames: names,
		Environment:   map[string]string{name: value},
	}), NewCallbacks(CallbacksConfig{
		WriteLog:          func(line string) error { logs.WriteString(line); return nil },
		TrackProcessGroup: func(int) { started = true },
	}))
	if !errors.Is(err, ErrInvalidStepExecution) {
		t.Fatalf("invalid environment did not fail preflight: %v", err)
	}
	if started || logs.Len() != 0 {
		t.Fatal("invalid environment reached an execution attempt")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("script started: %v", err)
	}
	if strings.Contains(err.Error(), "private") {
		t.Fatal("preflight error exposed a value")
	}
}

func TestExecutorPreservesValidHostEnvironment(t *testing.T) {
	for _, selected := range []bool{false, true} {
		name := strings.Repeat("A", 255)
		env := map[string]string{
			name: "boundary", "Mixed_2": "line1\nline2", "_EMPTY": "", "HOME": "host-home",
		}
		var names []string
		label := "unrestricted"
		if selected {
			names = []string{name, "Mixed_2", "_EMPTY", "HOME"}
			env["bad name"] = "unselected\x00value"
			label = "selected"
		}
		t.Run(label, func(t *testing.T) {
			var logs strings.Builder
			err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
				Name:          "valid environment",
				ScriptBody:    "test \"${" + name + "}\" = boundary && test \"$Mixed_2\" = 'line1\nline2' && test -z \"$_EMPTY\" && test \"$HOME\" = host-home && echo valid-entry",
				VariableNames: names, Environment: env,
			}), NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
				logs.WriteString(line)
				return nil
			}}))
			if err != nil || !strings.Contains(logs.String(), "valid-entry") {
				t.Fatalf("valid environment: %v, %s", err, logs.String())
			}
		})
	}
}
