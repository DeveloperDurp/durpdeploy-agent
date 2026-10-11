//go:build linux

package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestStagedHostAttemptStopsBackgroundWriters(t *testing.T) {
	for _, exit := range []int{0, 7} {
		t.Run(strconv.Itoa(exit), func(t *testing.T) {
			directory := t.TempDir()
			pidPath := filepath.Join(directory, "child")
			job := NewJob(JobConfig{
				StageDirectory: directory,
				ScriptBody: fmt.Sprintf(
					"sleep 30 >/dev/null 2>&1 &\necho $! > %q\nexit %d",
					pidPath, exit),
			})
			err := newExecutorForTest(t).Execute(t.Context(), job, Callbacks{})
			if (err != nil) != (exit != 0) {
				t.Fatalf("exit %d: %v", exit, err)
			}
			data, err := os.ReadFile(pidPath)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				process, err := os.FindProcess(pid)
				if err == nil {
					_ = process.Kill()
				}
			})
			deadline := time.Now().Add(time.Second)
			for {
				status, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
				if os.IsNotExist(err) ||
					strings.Contains(string(status), ") Z ") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("background writer survived attempt: %s", status)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
