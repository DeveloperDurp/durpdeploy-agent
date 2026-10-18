package artifact

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func zipFixture(t testing.TB, name string, mode os.FileMode) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write([]byte("package contents")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractZIPRejectsUnsafeEntries(t *testing.T) {
	for _, fixture := range []struct {
		name string
		mode os.FileMode
	}{
		{".", 0644}, {"../escape", 0644}, {"/absolute", 0644}, {"a\\escape", 0644},
		{"C:escape", 0644}, {"a/../escape", 0644}, {"link", os.ModeSymlink | 0644},
		{"pipe", os.ModeNamedPipe | 0644},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			// Given
			filename := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(
				filename,
				zipFixture(t, fixture.name, fixture.mode),
				0600,
			); err != nil {
				t.Fatal(err)
			}
			// When
			_, err := ExtractZIP(t.Context(), filename)
			// Then
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("expected invalid ZIP, got %v", err)
			}
		})
	}
}
