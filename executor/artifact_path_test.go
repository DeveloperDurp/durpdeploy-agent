package executor

import (
	"os"
	"path/filepath"
	"testing"
)

func TestArtifactPathIsAvailableWithVariableAllowlist(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("package", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir("stage", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("package/input", []byte("pinned"), 0444); err != nil {
		t.Fatal(err)
	}
	err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
		ArtifactDirectory: "package", StageDirectory: "stage",
		VariableNames: []string{"ALLOWED"},
		Environment:   map[string]string{"ALLOWED": "selected", "HIDDEN": "secret"},
		ScriptBody:    `test "$ALLOWED" = selected; test -z "${HIDDEN:-}"; cp "$ARTIFACT_PATH/input" "$DURPDEPLOY_STAGE_DIR/output"`,
	}), Callbacks{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("stage", "output"))
	if err != nil || string(data) != "pinned" {
		t.Fatalf("package output=%q error=%v", data, err)
	}
}
