package agentproto

import (
	"encoding/json"
)

type AgentID string
type AgentVersion string
type CertificatePEM string
type CertificateFingerprint string
type ClaimToken string
type DeploymentID int64
type LogSequence int64

type ProtocolEnvelope struct {
	Protocol ProtocolVersion `json:"protocol"`
}

func (e ProtocolEnvelope) protocolVersion() ProtocolVersion {
	return e.Protocol
}

// PairRequest initializes the server's mutually pinned pull endpoint at the
// agent after mutually authenticating the server certificate.
type PairRequest struct {
	ProtocolEnvelope
	PairingCode   PairingCode  `json:"pairing_code"`
	AgentPin      SHA256Pin    `json:"agent_pin"`
	ServerPin     SHA256Pin    `json:"server_pin"`
	PullEndpoint  PullEndpoint `json:"pull_endpoint"`
	AgentID       string       `json:"agent_id"`
	CompletionAck bool         `json:"completion_ack"`
}

func (PairRequest) agentRequest() {}

func (r PairRequest) validateMessage() error {
	if r.Protocol != AgentV1 {
		return protocolError(
			"protocol",
			ReasonInvalid,
			ErrUnsupportedProtocol,
		)
	}
	return nil
}

type PollRequest struct {
	ProtocolEnvelope
	AgentVersion          AgentVersion  `json:"agent_version"`
	SupportedInterpreters []Interpreter `json:"supported_interpreters,omitempty"`
	supportedPresent      bool
}

func (PollRequest) agentRequest() {}

func (r PollRequest) MarshalJSON() ([]byte, error) {
	type v1Poll struct {
		Protocol     ProtocolVersion `json:"protocol"`
		AgentVersion AgentVersion    `json:"agent_version"`
	}
	if r.Protocol == AgentV1 {
		return json.Marshal(v1Poll{r.Protocol, r.AgentVersion})
	}
	supported := r.SupportedInterpreters
	if supported == nil {
		supported = []Interpreter{}
	}
	type v2Poll struct {
		Protocol              ProtocolVersion `json:"protocol"`
		AgentVersion          AgentVersion    `json:"agent_version"`
		SupportedInterpreters []Interpreter   `json:"supported_interpreters"`
	}
	return json.Marshal(v2Poll{
		r.Protocol,
		r.AgentVersion,
		supported,
	})
}

func (r *PollRequest) UnmarshalJSON(data []byte) error {
	var wire struct {
		Protocol              ProtocolVersion `json:"protocol"`
		AgentVersion          AgentVersion    `json:"agent_version"`
		SupportedInterpreters json.RawMessage `json:"supported_interpreters"`
	}
	if err := jsonDecoder(data).Decode(&wire); err != nil {
		return err
	}
	r.Protocol = wire.Protocol
	r.AgentVersion = wire.AgentVersion
	r.supportedPresent = len(wire.SupportedInterpreters) != 0
	if !r.supportedPresent {
		r.SupportedInterpreters = nil
		return nil
	}
	if string(wire.SupportedInterpreters) == "null" {
		return protocolError(
			"supported_interpreters",
			ReasonInvalid,
			ErrInvalidJSON,
		)
	}
	return json.Unmarshal(wire.SupportedInterpreters, &r.SupportedInterpreters)
}

func (r PollRequest) validateMessage() error {
	if r.Protocol == AgentV1 && r.supportedPresent {
		return protocolError(
			"supported_interpreters",
			ReasonUnknown,
			ErrUnknownField,
		)
	}
	if r.Protocol == AgentV2 && !r.supportedPresent {
		return protocolError(
			"supported_interpreters",
			ReasonInvalid,
			ErrInvalidJSON,
		)
	}
	seen := make(map[Interpreter]struct{}, len(r.SupportedInterpreters))
	for _, interpreter := range r.SupportedInterpreters {
		if _, exists := seen[interpreter]; exists {
			return protocolError(
				"supported_interpreters",
				ReasonDuplicate,
				ErrDuplicateInterpreter,
			)
		}
		seen[interpreter] = struct{}{}
	}
	return nil
}

// PollResponse carries a single encrypted deployment payload and its
// one-time claim token. The server persists only a hash of ClaimToken.
type PollResponse struct {
	DeploymentID DeploymentID `json:"deployment_id"`
	Payload      string       `json:"payload"`
	ClaimToken   ClaimToken   `json:"claim_token"`
}

type StartRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (StartRequest) agentRequest() {}

type HeartbeatRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (HeartbeatRequest) agentRequest() {}

type HeartbeatResponse struct {
	CancelRequested bool                     `json:"cancel_requested"`
	ServerPins      []CertificateFingerprint `json:"server_pins"`
}

type LogEvent struct {
	Sequence LogSequence `json:"sequence"`
	Line     string      `json:"line"`
}

type LogBatchRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
	Events     []LogEvent `json:"events"`
}

func (LogBatchRequest) agentRequest() {}

type ResultState string

const (
	ResultSucceeded ResultState = "succeeded"
	ResultFailed    ResultState = "failed"
)

func (s *ResultState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("state", ReasonInvalid, ErrInvalidJSON)
	}
	if raw != string(ResultSucceeded) && raw != string(ResultFailed) {
		return protocolError("state", ReasonInvalid, ErrInvalidResultState)
	}
	*s = ResultState(raw)
	return nil
}

type ResultRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken  `json:"claim_token"`
	State      ResultState `json:"state"`
	Error      string      `json:"error"`
}

func (ResultRequest) agentRequest() {}

type CancelledRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (CancelledRequest) agentRequest() {}

type Request interface {
	agentRequest()
	protocolVersion() ProtocolVersion
}
