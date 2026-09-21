//go:build linux

package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestDiscoverInterpreters_reportsOnlyFixedAvailableExecutables(t *testing.T) {
	available := map[string]bool{"bash": true, "python3": true}

	got := discoverInterpreters(func(name string) (string, error) {
		if available[name] {
			return "/fixed/" + name, nil
		}
		return "", errors.New("not found")
	})

	want := []Interpreter{InterpreterBash, InterpreterPython3}
	if !slices.Equal(got, want) {
		t.Fatalf("interpreters = %q, want %q", got, want)
	}
}

func TestStepMarshalJSON_omitsBashForLegacyAgents(t *testing.T) {
	for _, interpreter := range []Interpreter{"", InterpreterBash} {
		encoded, err := json.Marshal(Step{
			Name:        "deploy",
			ScriptBody:  "echo ok",
			Interpreter: interpreter,
		})
		if err != nil {
			t.Fatalf("marshal step: %v", err)
		}
		if strings.Contains(string(encoded), "interpreter") {
			t.Fatalf("Bash payload includes interpreter: %s", encoded)
		}
	}
}

func TestStepMarshalJSON_includesNonBashInterpreter(t *testing.T) {
	encoded, err := json.Marshal(Step{Interpreter: InterpreterPython3})
	if err != nil {
		t.Fatalf("marshal step: %v", err)
	}
	if !strings.Contains(string(encoded), `"interpreter":"python3"`) {
		t.Fatalf("Python payload = %s, want interpreter", encoded)
	}
}

func TestExecutor_usesFixedInterpreterAndScratchExtension(t *testing.T) {
	tests := []struct {
		interpreter Interpreter
		extension   string
	}{
		{InterpreterBash, ".sh"},
		{InterpreterPwsh, ".ps1"},
		{InterpreterPython3, ".py"},
	}
	for _, test := range tests {
		t.Run(string(test.interpreter), func(t *testing.T) {
			binDir := t.TempDir()
			executable := filepath.Join(binDir, string(test.interpreter))
			wrapper := "#!/bin/sh\n" +
				"case \"$1\" in *" + test.extension + ") ;; *) exit 91 ;; esac\n" +
				"echo selected-" + string(test.interpreter) + "\n" +
				"exec /bin/sh \"$1\"\n"
			if err := os.WriteFile(executable, []byte(wrapper), 0o700); err != nil {
				t.Fatalf("write fake interpreter: %v", err)
			}
			t.Setenv("PATH", binDir)
			var logs []string

			err := newExecutorForTest(t).Execute(
				context.Background(),
				NewJob(JobConfig{
					Name:        "route",
					ScriptBody:  "echo script-ran",
					Interpreter: test.interpreter,
				}),
				NewCallbacks(CallbacksConfig{WriteLog: func(line string) error {
					logs = append(logs, line)
					return nil
				}}),
			)

			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			output := strings.Join(logs, "\n")
			if !strings.Contains(output, "selected-"+string(test.interpreter)) ||
				!strings.Contains(output, "script-ran") {
				t.Fatalf("logs = %q, want selected interpreter and script output", output)
			}
		})
	}
}

func TestExecutor_ExecuteSteps_runsMixedInterpretersInOrder(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "selected")
	tests := []struct {
		interpreter Interpreter
		extension   string
	}{
		{InterpreterBash, ".sh"},
		{InterpreterPwsh, ".ps1"},
		{InterpreterPython3, ".py"},
	}
	for _, test := range tests {
		executable := filepath.Join(binDir, string(test.interpreter))
		wrapper := "#!/bin/sh\n" +
			"case \"$1\" in *" + test.extension + ") ;; *) exit 91 ;; esac\n" +
			"echo " + string(test.interpreter) + " >> \"" + marker + "\"\n" +
			"exec /bin/sh \"$1\"\n"
		if err := os.WriteFile(executable, []byte(wrapper), 0o700); err != nil {
			t.Fatalf("write fake interpreter: %v", err)
		}
	}
	t.Setenv("PATH", binDir)
	steps := make([]Step, 0, len(tests))
	for index, test := range tests {
		steps = append(steps, Step{
			Name:        string(test.interpreter),
			ScriptBody:  "true",
			Interpreter: test.interpreter,
			SortOrder:   int64(index + 1),
		})
	}

	err := newExecutorForTest(t).ExecuteSteps(
		context.Background(),
		ExecutionConfig{DeploymentID: 1, Steps: steps},
	)

	if err != nil {
		t.Fatalf("execute mixed steps: %v", err)
	}
	selected, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("read selected interpreters: %v", err)
	}
	if got, want := string(selected), "bash\npwsh\npython3\n"; got != want {
		t.Fatalf("selected interpreters = %q, want %q", got, want)
	}
}

func TestExecutor_failsWithoutFallbackWhenInterpreterMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	err := newExecutorForTest(t).Execute(
		context.Background(),
		NewJob(JobConfig{
			Name:        "missing",
			ScriptBody:  "exit 0",
			Interpreter: InterpreterPwsh,
			MaxRetries:  2,
		}),
		NewCallbacks(CallbacksConfig{}),
	)

	if !errors.Is(err, ErrInterpreterUnavailable) ||
		!strings.Contains(err.Error(), "pwsh executable not found") {
		t.Fatalf("execute error = %v, want clear unavailable pwsh error", err)
	}
}
