package executor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExecutor_log_failure_does_not_repeat_host_side_effects(t *testing.T) {
	// Given
	marker := filepath.Join(t.TempDir(), "effects")
	job := NewJob(
		JobConfig{
			ScriptBody: fmt.Sprintf("echo effect >> %q; echo output", marker),
			MaxRetries: 2,
		},
	)
	calls := 0
	failure := errors.New("log upload timed out")
	callbacks := NewCallbacks(CallbacksConfig{WriteLog: func(string) error {
		calls++
		if calls == 1 {
			return failure
		}
		return nil
	}})
	// When
	err := newExecutorForTest(t).Execute(t.Context(), job, callbacks)
	// Then
	effects, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if !errors.Is(err, ErrLogDelivery) || !errors.Is(err, failure) ||
		strings.Count(string(effects), "effect\n") != 1 {
		t.Fatalf(
			"log failure repeated side effects: err=%v effects=%s",
			err,
			effects,
		)
	}
}
