package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestExecutor_ReturnsTimeout_when_logs_outlast_deadline_before_start(t *testing.T) {
	for _, mode := range []string{"host", "docker", "podman"} {
		t.Run(mode, func(t *testing.T) {
			// Allow process-based runtime preflight to finish on a busy runner.
			// The log callback still exceeds the actual step deadline below.
			const timeout = time.Second
			job := JobConfig{ScriptBody: "exit 0", Timeout: timeout, MaxRetries: 1}
			var executor *Executor
			var directory string
			if mode == "host" {
				executor = newExecutorForTest(t)
			} else {
				container, fixture := containerFixture(t, agentproto.ContainerRuntime(mode))
				executor, directory = NewExecutorWithContainers(container), fixture
				job.ExecutionMode, job.ContainerImage = agentproto.ExecutionContainer, "test/image"
			}
			attempts, started := 0, 0
			err := executor.Execute(t.Context(), NewJob(job), NewCallbacks(CallbacksConfig{
				WriteLog: func(line string) error {
					if strings.HasPrefix(line, "Execution location:") {
						attempts++
						// A successful cross-step log batch can outlast this step's deadline.
						time.Sleep(2 * timeout)
					}
					return nil
				},
				TrackProcessGroup: func(int) { started++ },
			}))
			if !errors.Is(err, ErrStepTimeout) || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("pre-start timeout lost identity: %v", err)
			}
			if started != 0 || attempts != 2 {
				t.Fatalf("started=%d attempts=%d, want 0 processes and 2 attempts", started, attempts)
			}
			if directory != "" {
				actions, err := os.ReadFile(filepath.Join(directory, "actions"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(actions), "run\n") || strings.Count(string(actions), "remove\n") != 2 {
					t.Fatalf("unexpected execution or cleanup: %s", actions)
				}
			}
		})
	}
}
