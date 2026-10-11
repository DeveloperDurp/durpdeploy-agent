package main

import (
	"crypto/sha256"
	"fmt"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func persistHostStagingCleanup(
	client *agentclient.Client,
	claim agentproto.PollResponse,
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
	}
	return writeClaimMarker(client, marker)
}
