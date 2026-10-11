package staging

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTripPrivateFilesAndEmptyDirectories(t *testing.T) {
	directory := t.TempDir()
	if err := os.MkdirAll(
		filepath.Join(directory, "nested", "empty"),
		0700,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, "nested", "data"),
		[]byte("contents"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	if err := Pack(t.Context(), directory, &archive); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := Unpack(
		t.Context(),
		bytes.NewReader(archive.Bytes()),
		output,
	); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(output, "nested", "data"))
	if err != nil || string(data) != "contents" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	info, err := os.Stat(filepath.Join(output, "nested", "empty"))
	if err != nil || !info.IsDir() {
		t.Fatalf("empty directory=%v err=%v", info, err)
	}
	info, err = os.Stat(filepath.Join(output, "nested", "data"))
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("private mode=%v err=%v", info, err)
	}
}

func TestArchivesRejectUnsafeEntries(t *testing.T) {
	for _, header := range []tar.Header{
		{Name: "../escape", Typeflag: tar.TypeReg},
		{Name: "/absolute", Typeflag: tar.TypeReg},
		{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"},
		{Name: "hardlink", Typeflag: tar.TypeLink, Linkname: "data"},
		{Name: "pipe", Typeflag: tar.TypeFifo},
		{Name: "big", Typeflag: tar.TypeReg, Size: MaxBytes + 1},
	} {
		t.Run(header.Name, func(t *testing.T) {
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			if err := writer.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
			if header.Size == 0 {
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := Unpack(
				t.Context(),
				bytes.NewReader(archive.Bytes()),
				t.TempDir(),
			); !errors.Is(
				err,
				ErrInvalid,
			) {
				t.Fatalf("unsafe entry err=%v", err)
			}
		})
	}
	directory := t.TempDir()
	if err := os.Symlink(
		"/etc/passwd",
		filepath.Join(directory, "link"),
	); err != nil {
		t.Fatal(err)
	}
	if err := Pack(
		t.Context(),
		directory,
		&bytes.Buffer{},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatalf("packing symlink err=%v", err)
	}
}

func TestPackHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := Pack(
		ctx,
		t.TempDir(),
		&bytes.Buffer{},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatal(err)
	}
}

func TestUnpackAcceptsContainerTarRoot(t *testing.T) {
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, header := range []tar.Header{{Name: "./", Typeflag: tar.TypeDir}, {Name: "./result", Typeflag: tar.TypeReg, Size: 2}} {
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := Unpack(
		t.Context(),
		bytes.NewReader(archive.Bytes()),
		"",
	); err != nil {
		t.Fatal(err)
	}
}
