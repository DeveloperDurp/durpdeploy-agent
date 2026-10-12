package executor

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestArtifactPathIsAvailableWithVariableAllowlist(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("package", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("package-output", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("package/input", []byte("pinned"), 0444); err != nil {
		t.Fatal(err)
	}
	err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
		ArtifactDirectory: "package", StageDirectory: "package-output",
		VariableNames: []string{"ALLOWED"},
		Environment:   map[string]string{"ALLOWED": "selected", "HIDDEN": "secret"},
		ScriptBody:    `test "$ALLOWED" = selected; test -z "${HIDDEN:-}"; cp "$ARTIFACT_PATH/input" "$DURPDEPLOY_STAGE_DIR/output"`,
	}), Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("package-output", "output"))
	if err != nil || string(data) != "pinned" {
		t.Fatalf("package output=%q error=%v", data, err)
	}
}

func TestArtifactRejectsSharedWritableStorage(t *testing.T) {
	// Given configured package and staging storage that share a backing path.
	root := t.TempDir()
	packagePath := filepath.Join(root, "package")
	for _, path := range []string{packagePath, filepath.Join(packagePath, "child")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(packagePath, alias); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		config JobConfig
	}{
		{"same-directory", JobConfig{ArtifactDirectory: packagePath, StageDirectory: packagePath}},
		{"stage-inside-package", JobConfig{ArtifactDirectory: packagePath, StageDirectory: filepath.Join(packagePath, "child")}},
		{"package-inside-stage", JobConfig{ArtifactDirectory: filepath.Join(packagePath, "child"), StageDirectory: packagePath}},
		{"symlink-alias", JobConfig{ArtifactDirectory: packagePath, StageDirectory: alias}},
		{"same-volume", JobConfig{ArtifactVolume: "shared", StageVolume: "shared", ExecutionMode: agentproto.ExecutionContainer, ContainerImage: "alpine:3.21"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "executed")
			scenario.config.ScriptBody = `touch "$MARKER"`
			scenario.config.Environment = map[string]string{"MARKER": marker}

			// When the public executor receives the configuration.
			err := newExecutorForTest(t).Execute(t.Context(), NewJob(scenario.config), Callbacks{})

			// Then it rejects the job before executing its script.
			if !errors.Is(err, ErrInvalidStepExecution) {
				t.Fatalf("accepted shared package storage: %v", err)
			}
			if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("invalid job executed: %v", err)
			}
		})
	}
}
