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

func stagingContainerFixture(
	t *testing.T,
	runtime agentproto.ContainerRuntime,
) (*ContainerExecutor, string) {
	t.Helper()
	runner, directory := containerFixture(t, runtime)
	binary := filepath.Join(directory, string(runtime))
	script, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	volume := `volume)
 printf 'volume %s\n' "$*" >> "$fixture/actions"
 if [[ -e "$fixture/volume-$1-fail" ]]; then exit 1; fi
 case "$1" in
 create)
  printf '%s' "${@: -1}" > "$fixture/volume"
  mkdir -p "$fixture/stage" ;;
 ls) [[ ! -e "$fixture/volume" ]] || cat "$fixture/volume" ;;
 rm) /usr/bin/rm -f "$fixture/volume" ;;
 esac ;;`
	execute := `exec)
 if [[ -e "$fixture/exec-fail" ]]; then exit 1; fi
 while [[ "$1" != tar ]]; do shift; done
 shift
 if [[ "$1" == -xf ]]; then
  /bin/tar -xf - -C "$fixture/stage"
 else /bin/tar -cf - -C "$fixture/stage" .; fi ;;
*) exit 2 ;;`
	updated := strings.Replace(string(script), "volume) ;;", volume, 1)
	updated = strings.Replace(updated, "*) exit 2 ;;", execute, 1)
	if err := os.WriteFile(binary, []byte(updated), 0755); err != nil {
		t.Fatal(err)
	}
	return runner, directory
}

func TestContainerStageTransfersPrivateWorkspaceAndPurges(t *testing.T) {
	for _, runtime := range []agentproto.ContainerRuntime{agentproto.RuntimeDocker, agentproto.RuntimePodman} {
		t.Run(string(runtime), func(t *testing.T) {
			runner, directory := stagingContainerFixture(t, runtime)
			input := t.TempDir()
			if err := os.WriteFile(
				filepath.Join(input, "artifact"),
				[]byte("input"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			var archive bytes.Buffer
			if err := staging.Pack(t.Context(), input, &archive); err != nil {
				t.Fatal(err)
			}
			stage, err := runner.NewStage(t.Context(), &archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(directory, "stage", "artifact"),
				[]byte("output"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := stage.Export(t.Context(), &output); err != nil {
				t.Fatal(err)
			}
			restored := t.TempDir()
			if err := staging.Unpack(
				t.Context(),
				&output,
				restored,
			); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(restored, "artifact"))
			if err != nil || string(data) != "output" {
				t.Fatalf("exported artifact=%q err=%v", data, err)
			}
			arguments, err := os.ReadFile(filepath.Join(directory, "argv"))
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"--network=none", "--user=65534:65534", "--cap-drop=ALL", stage.Volume + ":/stage:rw,nocopy"} {
				if !strings.Contains(string(arguments), expected) {
					t.Errorf("missing staging isolation: %s", expected)
				}
			}
			if err := stage.Close(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(
				filepath.Join(directory, "volume"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatal("retained staging volume", err)
			}
		})
	}
}

func TestContainerStageImportFailureRemovesPartialResources(t *testing.T) {
	for _, failure := range []string{"volume-create-fail", "run-fail", "exec-fail"} {
		t.Run(failure, func(t *testing.T) {
			runner, directory := stagingContainerFixture(
				t,
				agentproto.RuntimePodman,
			)
			if err := os.WriteFile(
				filepath.Join(directory, failure),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := runner.NewStage(
				t.Context(),
				bytes.NewReader(nil),
			); err == nil {
				t.Fatal("accepted failed stage import")
			}
			if _, err := os.Stat(
				filepath.Join(directory, "volume"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("partial stage retained volume: %v", err)
			}
		})
	}
}

func TestContainerStageCleanupFailureRequiresReconciliation(t *testing.T) {
	for _, failure := range []string{"volume-ls-fail", "volume-rm-fail", "cleanup-fail"} {
		t.Run(failure, func(t *testing.T) {
			runner, directory := stagingContainerFixture(
				t,
				agentproto.RuntimePodman,
			)
			var archive bytes.Buffer
			if err := staging.Pack(
				t.Context(),
				t.TempDir(),
				&archive,
			); err != nil {
				t.Fatal(err)
			}
			stage, err := runner.NewStage(t.Context(), &archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(directory, failure),
				nil,
				0600,
			); err != nil {
				t.Fatal(err)
			}
			if err := stage.Close(
				t.Context(),
			); !errors.Is(
				err,
				ErrContainerCleanup,
			) {
				t.Fatalf("cleanup error lost: %v", err)
			}
			if err := runner.Ready(
				t.Context(),
			); !errors.Is(
				err,
				ErrContainerCleanup,
			) {
				t.Fatalf("runtime resumed before cleanup: %v", err)
			}
			if err := os.Remove(filepath.Join(directory, failure)); err != nil {
				t.Fatal(err)
			}
			if err := runner.Ready(t.Context()); err != nil {
				t.Fatalf("runtime did not recover: %v", err)
			}
			if _, err := os.Stat(
				filepath.Join(directory, "volume"),
			); !errors.Is(
				err,
				os.ErrNotExist,
			) {
				t.Fatalf("reconciliation retained volume: %v", err)
			}
		})
	}
}
