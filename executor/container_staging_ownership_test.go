package executor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestContainerRejectsUnownedStagingVolumes(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{
		agentproto.RuntimeDocker, agentproto.RuntimePodman,
	} {
		t.Run(string(runtime), func(t *testing.T) {
			runner, directory := stagingContainerFixture(t, runtime)
			executor := NewExecutorWithContainers(runner)
			execute := func(volume string) error {
				return executor.Execute(t.Context(), NewJob(JobConfig{
					Name: "stage", ScriptBody: "true", StageVolume: volume,
					ExecutionMode:  agentproto.ExecutionContainer,
					ContainerImage: "alpine:3.21",
				}), Callbacks{})
			}
			for _, volume := range []string{"/", "../host", "other-volume"} {
				if err := execute(volume); !errors.Is(err, ErrInvalidStepExecution) {
					t.Errorf("accepted unowned volume %q: %v", volume, err)
				}
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if strings.Contains(string(actions), "run") {
				t.Fatal("launched a workload with an unowned mount")
			}
			var archive bytes.Buffer
			if err := staging.Pack(t.Context(), t.TempDir(), &archive); err != nil {
				t.Fatal(err)
			}
			stage, err := runner.NewStage(t.Context(), &archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := execute(stage.Volume); err != nil {
				t.Fatalf("owned stage rejected: %v", err)
			}
			volume := stage.Volume
			if err := stage.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := execute(volume); !errors.Is(err, ErrInvalidStepExecution) {
				t.Fatalf("closed stage accepted: %v", err)
			}
		})
	}
}
