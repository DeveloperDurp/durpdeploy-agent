package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Hold the kernel lease across reconnects and release it only when run exits.
// Never remove the lock file: other processes must lock the same inode.
func acquireStateLease(directory string) (*os.File, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(
		filepath.Join(directory, "agent.lock"),
		os.O_CREATE|os.O_RDWR,
		0600,
	)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(
		int(file.Fd()),
		syscall.LOCK_EX|syscall.LOCK_NB,
	); err != nil {
		return nil, errors.Join(
			fmt.Errorf("another agent owns this state directory: %w", err),
			file.Close(),
		)
	}
	return file, nil
}
