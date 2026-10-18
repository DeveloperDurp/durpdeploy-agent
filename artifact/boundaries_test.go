package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateZIPRejectsSizeAndTreeCollisions(t *testing.T) {
	for _, fixture := range []struct {
		name    string
		entries []zip.FileHeader
	}{
		{"declared bomb", []zip.FileHeader{{Name: "bomb", UncompressedSize64: uint64(MaxExtracted + 1)}}},
		{"file then child", []zip.FileHeader{{Name: "a"}, {Name: "a/b"}}},
		{"child then file", []zip.FileHeader{{Name: "a/b"}, {Name: "a"}}},
		{"duplicate", []zip.FileHeader{{Name: "a"}, {Name: "a"}}},
		{"duplicate directories", []zip.FileHeader{{Name: "a/"}, {Name: "a/"}}},
		{"directory child duplicate", []zip.FileHeader{{Name: "a/"}, {Name: "a/file"}, {Name: "a/"}}},
		{"implicit explicit duplicate", []zip.FileHeader{{Name: "a/file"}, {Name: "a/"}, {Name: "a/"}}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Given
			var buffer bytes.Buffer
			writer := zip.NewWriter(&buffer)
			for _, header := range fixture.entries {
				if _, err := writer.CreateRaw(&header); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(filename, buffer.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			// When
			err := ValidateZIP(t.Context(), filename)
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid ZIP: %v", err)
			}
		})
	}
}

func TestEntryHonorsCancellationDuringRead(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(t.Context())
	reader := contextReader{ctx: ctx, reader: strings.NewReader("contents")}
	cancel()
	// When
	_, err := reader.Read(make([]byte, 8))
	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestValidateZIPRejectsUnstageablePathLengths(t *testing.T) {
	for _, name := range []string{strings.Repeat("a", 256), strings.Repeat("dir/", 1001) + "file"} {
		t.Run(fmt.Sprint(len(name)), func(t *testing.T) {
			// Given
			filename := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(
				filename,
				zipFixture(t, name, 0644),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			// When
			err := ValidateZIP(t.Context(), filename)
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("unstageable path accepted: %v", err)
			}
		})
	}
}
