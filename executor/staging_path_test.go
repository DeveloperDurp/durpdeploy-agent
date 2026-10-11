package executor

import (
	"os"
	"path/filepath"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStagingResolvesRelativeHostDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, mode := range []agentproto.ExecutionMode{"", agentproto.ExecutionHost} {
		t.Run(string(mode), func(t *testing.T) {
			directory := string(mode) + "staging"
			if err := os.Mkdir(directory, 0700); err != nil {
				t.Fatal(err)
			}
			err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
				ExecutionMode:  mode,
				StageDirectory: directory,
				ScriptBody:     `printf artifact > "$DURPDEPLOY_STAGE_DIR/artifact"`,
			}), Callbacks{})
			if err != nil {
				t.Fatalf("relative staging job failed: %v", err)
			}
			data, err := os.ReadFile(filepath.Join(directory, "artifact"))
			if err != nil || string(data) != "artifact" {
				t.Fatalf("job did not use caller staging: %q %v", data, err)
			}
		})
	}
}
