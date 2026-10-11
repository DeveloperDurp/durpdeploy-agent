package staging

import (
	"archive/tar"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestUnpackCountsImplicitDirectories(t *testing.T) {
	for _, extra := range []string{"", "extra", "extra/data"} {
		t.Run("extra="+extra, func(t *testing.T) {
			// Given one implicit parent for each file in the archive.
			var archive bytes.Buffer
			writer := tar.NewWriter(&archive)
			for index := range MaxFiles / 2 {
				if err := writer.WriteHeader(&tar.Header{
					Name:     fmt.Sprintf("directory-%d/data", index),
					Typeflag: tar.TypeReg,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if extra != "" {
				if err := writer.WriteHeader(&tar.Header{
					Name: extra, Typeflag: tar.TypeReg,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}

			// When the archive is validated before import.
			err := Unpack(t.Context(), &archive, "")

			// Then implicit parents consume the same entry budget as headers.
			if extra != "" && !errors.Is(err, ErrInvalid) ||
				extra == "" && err != nil {
				t.Fatalf("extra=%q error=%v", extra, err)
			}
		})
	}
}

func TestUnpackRejectsImplicitParentConflicts(t *testing.T) {
	for _, headers := range [][]tar.Header{
		{
			{Name: "parent/data", Typeflag: tar.TypeReg},
			{Name: "parent", Typeflag: tar.TypeReg},
		},
		{
			{Name: "parent/data", Typeflag: tar.TypeReg},
			{Name: "parent", Typeflag: tar.TypeDir},
			{Name: "parent", Typeflag: tar.TypeDir},
		},
	} {
		// Given an archive that replaces a parent or repeats its declaration.
		var archive bytes.Buffer
		writer := tar.NewWriter(&archive)
		for _, header := range headers {
			if err := writer.WriteHeader(&header); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}

		// When the archive is validated.
		err := Unpack(t.Context(), &archive, "")

		// Then the conflicting archive remains invalid.
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("headers=%v error=%v", headers, err)
		}
	}
}

func TestUnpackAcceptsDeclaredImplicitParent(t *testing.T) {
	// Given a file followed by an explicit header for its implicit parent.
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, header := range []tar.Header{
		{Name: "parent/data", Typeflag: tar.TypeReg},
		{Name: "parent", Typeflag: tar.TypeDir},
	} {
		if err := writer.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()

	// When the archive is imported.
	err := Unpack(t.Context(), &archive, directory)

	// Then both headers refer to one directory and the file exists.
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(
		filepath.Join(directory, "parent", "data"),
	); err != nil {
		t.Fatal(err)
	}
}
