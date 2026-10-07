package agentclient

import (
	"context"
	"encoding/json"
	"fmt"

	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type cleanupReport struct {
	ServerURL string                   `json:"server_url"`
	AgentID   agentproto.AgentID       `json:"agent_id"`
	Result    agentproto.ResultRequest `json:"result"`
}

// SealCleanupReport retains claim authentication without plaintext tokens on disk.
func (client *Client) SealCleanupReport(
	claim agentproto.PollResponse,
) ([]byte, error) {
	report := cleanupReport{
		ServerURL: client.serverURL, AgentID: client.agentID,
		Result: agentproto.ResultRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			ClaimToken: claim.ClaimToken,
			State:      agentproto.ResultCleanupUnconfirmed,
			Error:      "Container cleanup could not be confirmed. Restore runtime access; the agent will reconcile its owned attempts and report this result before polling.",
		},
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	return agentpayload.Seal(
		client.identity.Certificate.Certificate[0],
		int64(claim.DeploymentID),
		raw,
	)
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
	raw, err := agentpayload.Open(client.identity, int64(id), sealed)
	if err != nil {
		return agentproto.ResultRequest{}, fmt.Errorf(
			"open pending cleanup report: %w",
			err,
		)
	}
	var report cleanupReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return agentproto.ResultRequest{}, fmt.Errorf(
			"decode pending cleanup report: %w",
			err,
		)
	}
	if report.ServerURL != client.serverURL ||
		report.AgentID != client.agentID ||
		report.Result.Protocol != agentproto.AgentV3 ||
		report.Result.State != agentproto.ResultCleanupUnconfirmed ||
		report.Result.ClaimToken == "" {
		return agentproto.ResultRequest{}, fmt.Errorf(
			"pending cleanup report does not match this pairing",
		)
	}
	return report.Result, nil
}
