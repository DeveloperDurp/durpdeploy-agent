// Package artifact validates bounded, pinned release ZIP packages.
package artifact

import (
	"encoding/hex"
	"errors"
	"fmt"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
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
	ErrMetadataLimit = fmt.Errorf("ZIP metadata read limit exceeded: %w", ErrInvalid)
)

// Pin is package identity authenticated by the encrypted job payload.
type Pin struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// DownloadRequest retains the artifact endpoint's envelope-free wire contract.
type DownloadRequest struct {
	ClaimToken agentproto.ClaimToken `json:"claim_token"`
}

func (p Pin) Validate() error {
	return p.ValidateWithPolicy(resourcepolicy.Defaults().Artifact)
}

// ValidateWithPolicy checks identity and size against a validated value snapshot.
func (p Pin) ValidateWithPolicy(policy resourcepolicy.Artifact) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	hash, err := hex.DecodeString(p.SHA256)
	if err != nil || len(hash) != 32 || p.Size <= 0 || p.Size > policy.DownloadBytes {
		return ErrInvalid
	}
	return nil
}
