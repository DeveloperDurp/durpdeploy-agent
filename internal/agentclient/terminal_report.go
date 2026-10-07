package agentclient

import (
	"context"
	"encoding/json"
	"fmt"

	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type terminalReport struct {
	ServerURL string                       `json:"server_url"`
	AgentID   agentproto.AgentID           `json:"agent_id"`
	Result    *agentproto.ResultRequest    `json:"result,omitempty"`
	Cancelled *agentproto.CancelledRequest `json:"cancelled,omitempty"`
}

// SealTerminalReport preserves the completed claim's outcome and wire protocol.
func (client *Client) SealTerminalReport(
	id agentproto.DeploymentID,
	request agentproto.Request,
) ([]byte, error) {
	var report terminalReport
	switch request := request.(type) {
	case agentproto.ResultRequest:
		if request.State != agentproto.ResultSucceeded &&
			request.State != agentproto.ResultFailed {
			return nil, fmt.Errorf(
				"ordinary terminal report requires succeeded or failed",
			)
		}
		request.Protocol = client.protocol
		report.Result = &request
	case agentproto.CancelledRequest:
		request.Protocol = client.protocol
		report.Cancelled = &request
	default:
		return nil, fmt.Errorf("unsupported terminal report request")
	}
	return client.sealReport(id, report)
}

func (client *Client) sealReport(
	id agentproto.DeploymentID,
	report terminalReport,
) ([]byte, error) {
	report.ServerURL, report.AgentID = client.serverURL, client.agentID
	raw, err := json.Marshal(report)
	if err != nil {
		return nil, err
	}
	return agentpayload.Seal(
		client.identity.Certificate.Certificate[0],
		int64(id),
		raw,
	)
}

// DecodeTerminalReport authenticates completed recovery state before delivery or retirement.
func (client *Client) DecodeTerminalReport(
	id agentproto.DeploymentID,
	sealed []byte,
) (agentproto.Request, error) {
	report, err := client.decodeReport(id, sealed)
	if err != nil {
		return nil, err
	}
	if report.Result != nil {
		if report.Result.State != agentproto.ResultSucceeded &&
			report.Result.State != agentproto.ResultFailed {
			return nil, fmt.Errorf(
				"ordinary terminal report contains an uncertain result",
			)
		}
		return *report.Result, nil
	}
	return *report.Cancelled, nil
}

// ReportTerminal sends an authenticated saved request without replacing its protocol.
func (client *Client) ReportTerminal(
	ctx context.Context,
	id agentproto.DeploymentID,
	request agentproto.Request,
) error {
	switch request := request.(type) {
	case agentproto.ResultRequest:
		return client.lifecycle(ctx, agentproto.ResultPath, id, request)
	case agentproto.CancelledRequest:
		return client.lifecycle(ctx, agentproto.CancelledPath, id, request)
	default:
		return fmt.Errorf("unsupported terminal report request")
	}
}

func (client *Client) decodeReport(
	id agentproto.DeploymentID,
	sealed []byte,
) (terminalReport, error) {
	raw, err := agentpayload.Open(client.identity, int64(id), sealed)
	if err != nil {
		return terminalReport{}, fmt.Errorf(
			"open pending terminal report: %w",
			err,
		)
	}
	var report terminalReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return terminalReport{}, fmt.Errorf(
			"decode pending terminal report: %w",
			err,
		)
	}
	if report.ServerURL != client.serverURL ||
		report.AgentID != client.agentID ||
		(report.Result == nil) == (report.Cancelled == nil) {
		return terminalReport{}, fmt.Errorf(
			"pending terminal report does not match this pairing",
		)
	}
	var protocol agentproto.ProtocolVersion
	var token agentproto.ClaimToken
	if report.Result != nil {
		protocol, token = report.Result.Protocol, report.Result.ClaimToken
		if report.Result.State == agentproto.ResultCleanupUnconfirmed &&
			protocol != agentproto.AgentV3 {
			return terminalReport{}, fmt.Errorf(
				"cleanup report requires agent/3",
			)
		}
	} else {
		protocol, token = report.Cancelled.Protocol, report.Cancelled.ClaimToken
	}
	if token == "" ||
		(protocol != agentproto.AgentV2 && protocol != agentproto.AgentV3) {
		return terminalReport{}, fmt.Errorf(
			"invalid terminal report authentication or protocol",
		)
	}
	return report, nil
}
