package staging

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPurgeRestrictiveDirectoriesAndSymlinks(t *testing.T) {
	outside := t.TempDir()
	data := filepath.Join(outside, "keep")
	if err := os.WriteFile(data, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "stage")
	nested := filepath.Join(directory, "locked", "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(nested, "secret"),
		[]byte("staged"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(nested, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(nested, 0000); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(nested), 0000); err != nil {
		t.Fatal(err)
	}
	if err := Purge(directory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging retained: %v", err)
	}
	contents, err := os.ReadFile(data)
	if err != nil || string(contents) != "outside" {
		t.Fatalf("outside data changed: %q %v", contents, err)
	}
	if err := Purge(directory); err != nil {
		t.Fatalf("repeat purge: %v", err)
	}
}
