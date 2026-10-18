//go:build linux

package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestScriptCleanupFailureBlocksRetries(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires the non-root agent identity to enforce directory permissions")
	}
	base := t.TempDir()
	t.Setenv("TMPDIR", base)
	t.Cleanup(func() {
		entries, err := os.ReadDir(base)
		if err != nil {
			t.Error(err)
			return
		}
		for _, entry := range entries {
			if entry.IsDir() {
				if err := os.Chmod(filepath.Join(base, entry.Name()), 0700); err != nil {
					t.Error(err)
				}
			}
		}
	})
	marker := filepath.Join(base, "attempts")
	err := newExecutorForTest(t).Execute(t.Context(), NewJob(JobConfig{
		ScriptBody: fmt.Sprintf("printf x >> %q\nchmod 000 .\n", marker),
		MaxRetries: 2,
	}), Callbacks{})
	contents, readErr := os.ReadFile(marker)
	if !errors.Is(err, ErrHostProcessCleanup) || !errors.Is(err, ErrContainerCleanup) || readErr != nil || string(contents) != "x" {
		t.Fatalf("cleanup failure retried a completed deployment: %v, attempts=%q, read=%v", err, contents, readErr)
	}
}
