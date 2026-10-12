// Package artifact validates bounded, pinned release ZIP packages.
package artifact

import (
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	MaxDownload      = int64(300 << 20)
	MaxExtracted     = int64(512 << 20)
	MaxFiles         = 10000
	MaxStagingNodes  = 19999
	MaxMetadata      = int64(16 << 20)
	PathVariable     = "ARTIFACT_PATH"
	CapabilityHeader = "X-DurpDeploy-Artifact"
	Capability       = "artifact/1"
)

var (
	ErrInvalid       = errors.New("invalid artifact repository or ZIP")
	ErrChecksum      = errors.New("artifact checksum mismatch")
	ErrMetadataLimit = fmt.Errorf("ZIP metadata reads exceed 16 MiB: %w", ErrInvalid)
)

// Pin is package identity authenticated by the encrypted job payload.
type Pin struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func (p Pin) Validate() error {
	hash, err := hex.DecodeString(p.SHA256)
	if err != nil || len(hash) != 32 || p.Size <= 0 || p.Size > MaxDownload {
		return ErrInvalid
	}
	return nil
}
