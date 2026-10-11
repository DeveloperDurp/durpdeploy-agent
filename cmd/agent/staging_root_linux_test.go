//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
)

func TestRecoveredStagingRootOwnership(t *testing.T) {
	for _, scenario := range []string{"purge", "replacement", "symlink", "missing", "invalid"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			original := filepath.Join(base, "staging")
			retained := filepath.Join(base, "retained")
			if err := os.Mkdir(original, 0700); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(original)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := root.WriteFile(
				"data",
				[]byte("owned"),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			if record, err := captureStagingRoot(
				root,
			); record != nil ||
				err != nil {
				t.Fatalf("unmoved root: %v %v", record, err)
			}
			if err := os.Rename(original, retained); err != nil {
				t.Fatal(err)
			}
			record, err := captureStagingRoot(root)
			if err != nil || record == nil || record.Path != retained {
				t.Fatalf("lost renamed root: %v %v", record, err)
			}
			if scenario == "purge" {
				for range 2 {
					if err := purgeRecoveredStagingRoot(record); err != nil {
						t.Fatal(err)
					}
				}
				entries, err := os.ReadDir(retained)
				if err != nil || len(entries) != 0 {
					t.Fatalf("retained files: %v %v", entries, err)
				}
				return
			}
			moved := filepath.Join(base, "moved")
			if err := os.Rename(retained, moved); err != nil {
				t.Fatal(err)
			}
			if scenario == "replacement" {
				if err := os.Mkdir(retained, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					filepath.Join(retained, "private"),
					[]byte("unrelated"),
					0600,
				); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "symlink" {
				if err := os.Symlink(moved, retained); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "invalid" {
				record = &stagingRootRecord{}
			}
			err = purgeRecoveredStagingRoot(record)
			if scenario == "missing" {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("missing root accepted: %v", err)
				}
			} else if !errors.Is(err, staging.ErrInvalid) {
				t.Fatalf("replacement accepted: %v", err)
			}
			if data, err := os.ReadFile(
				filepath.Join(moved, "data"),
			); err != nil ||
				string(data) != "owned" {
				t.Fatalf("deleted uncertain files: %q %v", data, err)
			}
			if scenario == "replacement" {
				if data, err := os.ReadFile(
					filepath.Join(retained, "private"),
				); err != nil ||
					string(data) != "unrelated" {
					t.Fatalf("deleted unrelated files: %q %v", data, err)
				}
			}
		})
	}
}
