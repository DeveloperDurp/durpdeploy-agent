package agentclient

import (
	"context"
	"fmt"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// SealCleanupReport retains claim authentication without plaintext tokens on disk.
func (client *Client) SealCleanupReport(
	claim agentproto.PollResponse,
) ([]byte, error) {
	return client.sealReport(claim.DeploymentID, terminalReport{
		Result: &agentproto.ResultRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			ClaimToken: claim.ClaimToken,
			State:      agentproto.ResultCleanupUnconfirmed,
			Error:      "Container cleanup could not be confirmed. Restore runtime access; the agent will reconcile its owned attempts and report this result before polling.",
		},
	})
}

// ReportCleanup replays a sealed terminal report only to its original pairing.
func (client *Client) ReportCleanup(
	ctx context.Context,
	id agentproto.DeploymentID,
	sealed []byte,
) error {
	result, err := client.DecodeCleanupReport(id, sealed)
	if err != nil {
		return err
	}
	return client.lifecycle(ctx, agentproto.ResultPath, id, result)
}

// DecodeCleanupReport authenticates recovery state, including acknowledged reports.
func (client *Client) DecodeCleanupReport(
	id agentproto.DeploymentID,
	sealed []byte,
) (agentproto.ResultRequest, error) {
	report, err := client.decodeReport(id, sealed)
	if err != nil {
		return agentproto.ResultRequest{}, err
	}
	if report.Result == nil ||
		report.Result.Protocol != agentproto.AgentV3 ||
		report.Result.State != agentproto.ResultCleanupUnconfirmed {
		return agentproto.ResultRequest{}, fmt.Errorf(
			"pending cleanup report does not match this pairing",
		)
	}
	return *report.Result, nil
}
