//go:build !linux

package main

import (
	"os"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
)

func captureStagingRoot(*os.Root) (*stagingRootRecord, error) {
	return nil, staging.ErrInvalid
}

func purgeRecoveredStagingRoot(record *stagingRootRecord) error {
	if record == nil {
		return nil
	}
	return staging.ErrInvalid
}
