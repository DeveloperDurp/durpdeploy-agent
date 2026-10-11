package main

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func reportHostStagingCleanup(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) error {
	sealed, err := client.SealCleanupReport(claim)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	marker := claimMarker{
		DeploymentID:       int64(claim.DeploymentID),
		TokenHash:          fmt.Sprintf("%x", digest),
		CleanupResult:      sealed,
		HostStagingCleanup: true,
	}
	if err := writeClaimMarker(client, marker); err != nil {
		return err
	}
	if err := client.ReportCleanup(
		ctx,
		claim.DeploymentID,
		sealed,
	); err != nil {
		return err
	}
	marker.CleanupAcknowledged = true
	return writeClaimMarker(client, marker)
}
