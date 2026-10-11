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
	walkErr := fs.WalkDir(
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
	)
	return errors.Join(walkErr, root.Close(), os.RemoveAll(directory))
}
