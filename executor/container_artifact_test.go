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

func stageTestArchive(t *testing.T) *bytes.Buffer {
	t.Helper()
	input := t.TempDir()
	if err := os.WriteFile(filepath.Join(input, "input"), []byte("pinned"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := staging.Pack(t.Context(), input, &archive); err != nil {
		t.Fatal(err)
	}
	return &archive
}

func TestArtifactStageHandoverAndWritableAliasRejection(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			runner, directory := stagingContainerFixture(t, runtime)
			stage, err := runner.NewArtifactStage(t.Context(), stageTestArchive(t))
			if err != nil {
				t.Fatal(err)
			}
			actions, err := os.ReadFile(filepath.Join(directory, "actions"))
			if err != nil {
				t.Fatal(err)
			}
			last := -1
			for _, action := range []string{"run-name " + stage.importer, "import-export", "run-name " + stage.keeper, "remove-name " + stage.importer} {
				next := strings.Index(string(actions), action)
				if next <= last {
					t.Fatalf("handover order missing %q: %s", action, actions)
				}
				last = next
			}
			if _, err := os.Stat(filepath.Join(directory, "containers", stage.importer)); !os.IsNotExist(err) {
				t.Fatalf("writable importer remains: %v", err)
			}
			job := JobConfig{ScriptBody: "true", StageVolume: stage.Volume,
				ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "alpine:3.21"}
			if err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(job), Callbacks{}); !errors.Is(err, ErrInvalidStepExecution) {
				t.Fatalf("artifact attached as writable staging: %v", err)
			}
			job.StageVolume, job.ArtifactVolume = "", stage.Volume
			if err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(job), Callbacks{}); err != nil {
				t.Fatal(err)
			}
			if err := stage.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(job), Callbacks{}); !errors.Is(err, ErrInvalidStepExecution) {
				t.Fatalf("closed artifact accepted: %v", err)
			}
		})
	}
}

func TestArtifactStagePartialImportAndCleanupRecovery(t *testing.T) {
	for _, failure := range []string{"volume-create-fail", "run-fail", "exec-fail", "seal-fail", "cleanup-fail"} {
		t.Run(failure, func(t *testing.T) {
			runner, directory := stagingContainerFixture(t, agentproto.RuntimePodman)
			if err := os.WriteFile(filepath.Join(directory, failure), nil, 0600); err != nil {
				t.Fatal(err)
			}
			stage, err := runner.NewArtifactStage(t.Context(), stageTestArchive(t))
			if err == nil || len(runner.stages) != 0 {
				t.Fatalf("partial artifact became usable: stage=%+v err=%v", stage, err)
			}
			if failure == "cleanup-fail" {
				if !errors.Is(err, ErrContainerCleanup) || !errors.Is(runner.Ready(t.Context()), ErrContainerCleanup) {
					t.Fatal("unconfirmed importer cleanup did not block recovery")
				}
			}
			if err := os.Remove(filepath.Join(directory, failure)); err != nil {
				t.Fatal(err)
			}
			if err := runner.Ready(t.Context()); err != nil {
				t.Fatal(err)
			}
			containers, err := runner.ownedContainers(t.Context())
			if err != nil || len(containers) != 0 {
				t.Fatalf("recovery retained helpers: %v %v", containers, err)
			}
		})
	}
}

func TestArtifactStageRequiresConfirmedVolumeRemoval(t *testing.T) {
	runner, directory := stagingContainerFixture(t, agentproto.RuntimePodman)
	stage, err := runner.NewArtifactStage(t.Context(), stageTestArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "volume-rm-retain"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := stage.Close(t.Context()); !errors.Is(err, ErrContainerCleanup) {
		t.Fatalf("retained volume reported removed: %v", err)
	}
	if err := runner.Ready(t.Context()); !errors.Is(err, ErrContainerCleanup) {
		t.Fatalf("retained volume permitted polling: %v", err)
	}
	if err := os.Remove(filepath.Join(directory, "volume-rm-retain")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestArtifactStageRegistrationSurvivesHandleOverwrite(t *testing.T) {
	runner, _ := stagingContainerFixture(t, agentproto.RuntimePodman)
	stage, err := runner.NewArtifactStage(t.Context(), stageTestArchive(t))
	if err != nil {
		t.Fatal(err)
	}
	// These operations are available to callers outside executor without unsafe.
	saved := *stage
	*stage = ContainerStage{}
	defer func() {
		if err := saved.Close(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	err = NewExecutorWithContainers(runner).Execute(t.Context(), NewJob(JobConfig{
		StageVolume: saved.Volume, ScriptBody: "true", ExecutionMode: agentproto.ExecutionContainer,
		ContainerImage: "alpine:3.21",
	}), Callbacks{})
	if !errors.Is(err, ErrInvalidStepExecution) {
		t.Fatalf("overwriting a handle made its artifact writable: %v", err)
	}
	var output bytes.Buffer
	if err := saved.Export(t.Context(), &output); err != nil {
		t.Fatalf("copied public handle cannot export its owned volume: %v", err)
	}
}
