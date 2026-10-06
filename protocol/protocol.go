package agentproto

import (
	"encoding/json"
	"time"
)

type ProtocolVersion string

const (
	AgentV1 ProtocolVersion = "agent/1"
	AgentV2 ProtocolVersion = "agent/2"
	AgentV3 ProtocolVersion = "agent/3"

	MaxRequestBytes        = 1 << 20
	MaxLogEvents           = 100
	MaxLogBatchBytes       = 256 << 10
	MaxLogLineBytes        = 16 << 10
	MaxPairingMessageBytes = 64 << 10

	PollInterval                 = 25 * time.Second
	HeartbeatInterval            = 10 * time.Second
	PreStartClaimTimeout         = 60 * time.Second
	LostThreshold                = 45 * time.Second
	CancelAcknowledgementTimeout = 30 * time.Second
)

const (
	ServerInitPath = "/agent/v1/pairings/server-init"
	PollPath       = "/agent/v1/poll"
	DeploymentPath = "/agent/v1/deployments/{id}"
	StartPath      = DeploymentPath + "/start"
	HeartbeatPath  = DeploymentPath + "/heartbeat"
	LogsPath       = DeploymentPath + "/logs"
	ResultPath     = DeploymentPath + "/result"
	CancelledPath  = DeploymentPath + "/cancelled"
)

func ParseProtocolVersion(raw string) (ProtocolVersion, error) {
	version := ProtocolVersion(raw)
	if version != AgentV1 && version != AgentV2 && version != AgentV3 {
		return "", protocolError(
			"protocol",
			ReasonInvalid,
			ErrUnsupportedProtocol,
		)
	}
	return version, nil
}

func (v *ProtocolVersion) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("protocol", ReasonInvalid, ErrInvalidJSON)
	}

	parsed, err := ParseProtocolVersion(raw)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}
