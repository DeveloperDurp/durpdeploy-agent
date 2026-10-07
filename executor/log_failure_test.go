package executor

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainer_log_delivery_failure_stops_retries(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			// Given
			runner, directory := containerFixture(t, runtime)
			calls := 0
			failure := errors.New("log endpoint unavailable")
			callbacks := NewCallbacks(
				CallbacksConfig{WriteLog: func(string) error {
					calls++
					if calls == 1 {
						return failure
					}
					return nil
				}},
			)
			// When
			err := NewExecutorWithContainers(
				runner,
			).Execute(t.Context(), NewJob(JobConfig{
				ScriptBody:     "exit 0",
				ExecutionMode:  agentproto.ExecutionContainer,
				ContainerImage: "test/image",
				MaxRetries:     2,
			}), callbacks)
			// Then
			actions, readErr := os.ReadFile(filepath.Join(directory, "actions"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !errors.Is(err, failure) ||
				strings.Count(string(actions), "run\n") != 1 ||
				strings.Count(string(actions), "remove\n") != 1 {
				t.Fatalf(
					"log failure reran work: err=%v actions=%s",
					err,
					actions,
				)
			}
		})
	}
}
