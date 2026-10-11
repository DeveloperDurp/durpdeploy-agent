//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
)

func captureStagingRoot(
	root *os.Root,
) (record *stagingRootRecord, result error) {
	if err := root.Chmod(".", 0700); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	file, err := root.Open(".")
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	current, err := os.Lstat(root.Name())
	if err == nil && os.SameFile(info, current) {
		return nil, nil
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, staging.ErrInvalid
	}
	if identity.Nlink == 0 {
		return nil, nil
	}
	path, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
	if err != nil {
		return nil, err
	}
	return &stagingRootRecord{path, uint64(identity.Dev), identity.Ino}, nil
}

func purgeRecoveredStagingRoot(record *stagingRootRecord) (result error) {
	if record == nil {
		return nil
	}
	if !filepath.IsAbs(record.Path) || record.Inode == 0 {
		return staging.ErrInvalid
	}
	info, err := os.Lstat(record.Path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return staging.ErrInvalid
	}
	root, err := os.OpenRoot(record.Path)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, root.Close()) }()
	info, err = root.Stat(".")
	if err != nil {
		return err
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || uint64(identity.Dev) != record.Device ||
		identity.Ino != record.Inode {
		return staging.ErrInvalid
	}
	// Keep the empty directory for idempotent recovery if marker persistence fails.
	return staging.PurgeRoot(root)
}
