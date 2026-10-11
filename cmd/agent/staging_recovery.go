package main

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type stagingRootRecord struct {
	Path   string `json:"path"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func persistHostStagingCleanup(
	client *agentclient.Client,
	claim agentproto.PollResponse,
	roots []*stagingRootRecord,
) error {
	sealed, err := client.SealTerminalReport(claim.DeploymentID,
		agentproto.ResultRequest{
			ClaimToken: claim.ClaimToken, State: agentproto.ResultFailed,
		})
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	marker := claimMarker{
		DeploymentID:       int64(claim.DeploymentID),
		TokenHash:          fmt.Sprintf("%x", digest),
		TerminalReport:     sealed,
		HostStagingCleanup: true,
		HostStagingRoots:   roots,
	}
	return writeClaimMarker(client, marker)
}

func (s *jobStaging) recordRoots() error {
	s.recoveryRoots = nil
	base := ""
	for _, root := range []*os.File{s.root, s.workspace} {
		if root == nil {
			continue
		}
		record, err := captureStagingRoot(root)
		if err != nil {
			// An unavailable identity must block replay rather than skip cleanup.
			s.recoveryRoots = []*stagingRootRecord{{}}
			return err
		}
		if root == s.root {
			base = root.Name()
			if record != nil {
				base = record.Path
			}
		} else if record != nil {
			relative, err := filepath.Rel(base, record.Path)
			if err == nil && relative != ".." &&
				!strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue // The staging root already owns this recovery obligation.
			}
		}
		if record != nil {
			s.recoveryRoots = append(s.recoveryRoots, record)
		}
	}
	return nil
}
