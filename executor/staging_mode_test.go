package executor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStagingRejectsOppositeExecutionMode(t *testing.T) {
	for _, config := range []JobConfig{
		{StageDirectory: t.TempDir(), ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "alpine:3.21"},
		{StageVolume: "stage-volume", ExecutionMode: agentproto.ExecutionHost},
		{StageVolume: "stage-volume"},
		{StageDirectory: t.TempDir(), StageVolume: "stage-volume"},
	} {
		t.Run(
			string(config.ExecutionMode)+config.StageDirectory,
			func(t *testing.T) {
				marker := filepath.Join(t.TempDir(), "executed")
				config.ScriptBody = `touch "$MARKER"`
				config.Environment = map[string]string{"MARKER": marker}
				config.MaxRetries = 2
				if err := newExecutorForTest(
					t,
				).Execute(t.Context(), NewJob(config), Callbacks{}); !errors.Is(
					err,
					ErrInvalidStepExecution,
				) {
					t.Fatalf(
						"accepted staging handle for opposite mode: %v",
						err,
					)
				}
				if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("invalid job executed: %v", err)
				}
			},
		)
	}
}
