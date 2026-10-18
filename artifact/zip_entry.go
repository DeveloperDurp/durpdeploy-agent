package artifact

import (
	"archive/zip"
	"io/fs"
	"path"
	"strings"
)

// Staging uses Linux filesystems: 255-byte components and room for mount prefixes.
const maxZIPPathBytes = 4000
const maxZIPComponentBytes = 255

type zipNodeKind uint8

const (
	zipImplicitDirectory zipNodeKind = iota
	zipDirectory
	zipFile
)

func validateZIPEntry(entry *zip.File, kinds map[string]zipNodeKind) error {
	name, err := zipEntryName(entry.Name)
	if err != nil {
		return err
	}
	mode := entry.Mode()
	if !mode.IsRegular() && !mode.IsDir() {
		return ErrInvalid
	}
	if kind, exists := kinds[name]; exists &&
		(kind != zipImplicitDirectory || !mode.IsDir()) {
		return ErrInvalid
	}
	kinds[name] = zipFile
	if mode.IsDir() {
		kinds[name] = zipDirectory
	}
	if len(kinds) > MaxStagingNodes {
		return ErrInvalid
	}
	for parent := path.Dir(name); parent != "."; parent = path.Dir(parent) {
		if kind, exists := kinds[parent]; exists {
			if kind == zipFile {
				return ErrInvalid
			}
		} else {
			kinds[parent] = zipImplicitDirectory
		}
		if len(kinds) > MaxStagingNodes {
			return ErrInvalid
		}
	}
	return nil
}

func zipEntryName(raw string) (string, error) {
	name := strings.TrimSuffix(raw, "/")
	if len(name) > maxZIPPathBytes {
		return "", ErrInvalid
	}
	for _, component := range strings.Split(name, "/") {
		if len(component) > maxZIPComponentBytes {
			return "", ErrInvalid
		}
	}
	if name == "." || !fs.ValidPath(name) || path.Clean(name) != name ||
		strings.ContainsAny(name, "\\:\x00") {
		return "", ErrInvalid
	}
	return name, nil
}
