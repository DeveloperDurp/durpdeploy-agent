package staging

import (
	"errors"
	"io/fs"
	"os"
)

// Purge restores directory access when a completed script changed permissions.
// WalkDir does not follow symlinks. Call only after the job's processes stop.
func Purge(directory string) error {
	if err := os.RemoveAll(directory); err == nil {
		return nil
	}
	info, err := os.Lstat(directory)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.RemoveAll(directory)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	return errors.Join(PurgeRoot(root), root.Close(), os.RemoveAll(directory))
}

// PurgeRoot removes owned contents through a handle that survives renaming.
// Call only after the job's processes stop.
func PurgeRoot(root *os.Root) error {
	if err := root.Chmod(".", 0700); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if err := fs.WalkDir(
		root.FS(),
		".",
		func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return root.Chmod(name, 0700)
			}
			return nil
		},
	); err != nil {
		return err
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return err
	}
	var result error
	for _, entry := range entries {
		result = errors.Join(result, root.RemoveAll(entry.Name()))
	}
	return result
}
