package artifact

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteTarPreservesPackageContentsWithFixedMetadata(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "nested", "input"), []byte("package"), 0600); err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	if err := WriteTar(t.Context(), directory, &buffer); err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(&buffer)
	for _, name := range []string{"nested", "nested/input"} {
		header, err := reader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != name || header.Uid != 65534 || header.Gid != 65534 {
			t.Fatalf("unexpected header: %+v", header)
		}
		mode := int64(0755)
		if name == "nested/input" {
			mode = 0644
		}
		if header.Mode != mode {
			t.Fatalf("mode=%o want=%o", header.Mode, mode)
		}
	}
	contents, err := io.ReadAll(reader)
	if err != nil || string(contents) != "package" {
		t.Fatalf("contents=%q error=%v", contents, err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("extra entry: %v", err)
	}
}

func TestWriteTarRejectsLinksAndCancellation(t *testing.T) {
	directory := t.TempDir()
	if err := os.Symlink("../outside", filepath.Join(directory, "link")); err != nil {
		t.Fatal(err)
	}
	if err := WriteTar(t.Context(), directory, io.Discard); !errors.Is(err, ErrInvalid) {
		t.Fatalf("link error=%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := WriteTar(ctx, t.TempDir(), io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
}
