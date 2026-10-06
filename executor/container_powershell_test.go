package executor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

var powerShellScripts = []struct {
	name, script, output string
	exitCode             int
}{
	{
		"multiline",
		"Write-Output 'BEFORE'\nif ($true) {\n    Write-Output 'INSIDE'\n}\nWrite-Output 'AFTER'\n",
		"BEFORE\nINSIDE\nAFTER",
		0,
	},
	{
		"throw",
		"Write-Output 'ATTEMPT'\nthrow 'deployment failed'\nWrite-Output 'UNREACHABLE'\n",
		"ATTEMPT",
		1,
	},
	{
		"exit",
		"Write-Output 'ATTEMPT'\nexit 23\nWrite-Output 'UNREACHABLE'\n",
		"ATTEMPT",
		23,
	},
}

func TestContainer_powershell_executes_complete_scripts(t *testing.T) {
	pwsh, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip(
			"local PowerShell unavailable; both live engine contracts cover this path",
		)
	}
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		for _, script := range powerShellScripts {
			t.Run(string(runtime)+"/"+script.name, func(t *testing.T) {
				// Given
				runner, directory := containerFixture(t, runtime)
				if err := os.WriteFile(
					filepath.Join(directory, "pwsh"),
					[]byte(pwsh),
					0600,
				); err != nil {
					t.Fatal(err)
				}
				var logs []string
				job := NewJob(JobConfig{
					Interpreter:    InterpreterPwsh,
					ScriptBody:     script.script,
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "test/pwsh",
					MaxRetries:     1,
					Timeout:        time.Minute,
				})
				// When
				err := NewExecutorWithContainers(
					runner,
				).Execute(t.Context(), job, NewCallbacks(CallbacksConfig{
					WriteLog: func(line string) error { logs = append(logs, line); return nil },
				}))
				// Then
				assertPowerShellScriptResult(
					t,
					strings.Join(logs, "\n"),
					script.output,
					script.exitCode,
					err,
				)
				actions, err := os.ReadFile(filepath.Join(directory, "actions"))
				if err != nil {
					t.Fatal(err)
				}
				attempts := 1
				if script.exitCode != 0 {
					attempts = 2
				}
				if strings.Count(string(actions), "run\n") != attempts ||
					strings.Count(string(actions), "remove\n") != attempts {
					t.Fatalf("attempt/cleanup count: %s", actions)
				}
			})
		}
	}
}

func assertPowerShellScriptResult(
	t *testing.T,
	output, expected string,
	exitCode int,
	err error,
) {
	t.Helper()
	attempts := 1
	if exitCode != 0 {
		attempts = 2
	}
	if (err == nil) != (exitCode == 0) ||
		strings.Count(output, expected) != attempts ||
		strings.Contains(output, "UNREACHABLE") {
		t.Fatalf("PowerShell result: %v, output: %s", err, output)
	}
	var exitErr *exec.ExitError
	if exitCode != 0 &&
		(!errors.As(err, &exitErr) || exitErr.ExitCode() != exitCode) {
		t.Fatalf("PowerShell exit code lost: %v", err)
	}
}
