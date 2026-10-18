package artifact

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzExtractZIP(f *testing.F) {
	f.Add(zipFixture(f, "nested/package.txt", 0755))
	f.Add(zipFixture(f, "../outside", 0644))
	f.Add(zipFixture(f, "link", os.ModeSymlink|0644))
	f.Add([]byte("not a ZIP"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		const budget = 64 << 10
		if len(data) > budget {
			t.Skip()
		}
		// Bound decompression and filesystem work per mutation. Production limit
		// and compression-bomb rejection are covered by the boundary unit tests.
		if archive, _ := zip.NewReader(bytes.NewReader(data), int64(len(data))); archive != nil {
			if len(archive.File) > 16 {
				t.Skip()
			}
			var total uint64
			for _, entry := range archive.File {
				if entry.UncompressedSize64 > budget-total {
					t.Skip()
				}
				total += entry.UncompressedSize64
			}
		}
		root := t.TempDir()
		filename := filepath.Join(root, "input.zip")
		outside := filepath.Join(root, "outside")
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outside, []byte("sentinel"), 0600); err != nil {
			t.Fatal(err)
		}
		directory, extractErr := ExtractZIP(t.Context(), filename)
		contents, err := os.ReadFile(outside)
		if err != nil || string(contents) != "sentinel" {
			t.Fatalf("extraction changed a file outside staging: %v", err)
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		if extractErr != nil {
			if directory != "" || len(entries) != 2 {
				t.Fatal("failed extraction left staging files behind")
			}
			return
		}
		if filepath.Dir(directory) != root || !strings.HasPrefix(filepath.Base(directory), "durpdeploy-artifact-") || len(entries) != 3 {
			t.Fatal("extraction escaped its private staging directory")
		}
		if err := ValidateZIP(t.Context(), filename); err != nil {
			t.Fatalf("extracted an archive rejected by validation: %v", err)
		}
		if err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 != 0) {
				t.Errorf("unsafe extracted file mode at %s: %s", name, info.Mode())
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}
