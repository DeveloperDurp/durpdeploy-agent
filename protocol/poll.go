package agentproto

import "encoding/json"

type PollRequest struct {
	ProtocolEnvelope
	ExecutionCapabilities
	AgentVersion          AgentVersion  `json:"agent_version"`
	SupportedInterpreters []Interpreter `json:"supported_interpreters,omitempty"`
	supportedPresent      bool
	executionPresent      bool
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
	if r.Protocol == AgentV3 {
		type v3Poll struct {
			ProtocolEnvelope
			ExecutionCapabilities
			AgentVersion          AgentVersion  `json:"agent_version"`
			SupportedInterpreters []Interpreter `json:"supported_interpreters"`
		}
		capabilities := r.ExecutionCapabilities
		if capabilities.ContainerRuntimes == nil {
			capabilities.ContainerRuntimes = []ContainerRuntime{}
		}
		if capabilities.ContainerInterpreters == nil {
			capabilities.ContainerInterpreters = []Interpreter{}
		}
		if capabilities.ExecutionModes == nil {
			capabilities.ExecutionModes = []ExecutionMode{}
		}
		supported := r.SupportedInterpreters
		if supported == nil {
			supported = []Interpreter{}
		}
		return json.Marshal(
			v3Poll{r.ProtocolEnvelope, capabilities, r.AgentVersion, supported},
		)
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
		ExecutionModes        json.RawMessage `json:"execution_modes"`
		ContainerRuntimes     json.RawMessage `json:"container_runtimes"`
		ContainerInterpreters json.RawMessage `json:"container_interpreters"`
	}
	if err := jsonDecoder(data).Decode(&wire); err != nil {
		return err
	}
	r.Protocol = wire.Protocol
	r.AgentVersion = wire.AgentVersion
	r.ExecutionCapabilities = ExecutionCapabilities{}
	r.executionPresent = len(wire.ExecutionModes) != 0 ||
		len(wire.ContainerRuntimes) != 0 ||
		len(wire.ContainerInterpreters) != 0
	if r.Protocol != AgentV3 && r.executionPresent {
		return protocolError("capabilities", ReasonUnknown, ErrUnknownField)
	}
	if r.Protocol == AgentV3 {
		for _, raw := range []json.RawMessage{wire.ExecutionModes, wire.ContainerRuntimes, wire.ContainerInterpreters} {
			if len(raw) == 0 || string(raw) == "null" {
				return protocolError(
					"capabilities",
					ReasonInvalid,
					ErrInvalidJSON,
				)
			}
		}
		if err := json.Unmarshal(
			wire.ExecutionModes,
			&r.ExecutionModes,
		); err != nil {
			return err
		}
		if err := json.Unmarshal(
			wire.ContainerRuntimes,
			&r.ContainerRuntimes,
		); err != nil {
			return err
		}
		if err := json.Unmarshal(
			wire.ContainerInterpreters,
			&r.ContainerInterpreters,
		); err != nil {
			return err
		}
	}
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
	if (r.Protocol == AgentV2 || r.Protocol == AgentV3) && !r.supportedPresent {
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
	if r.Protocol == AgentV3 {
		return r.ExecutionCapabilities.validate(r.SupportedInterpreters)
	}
	return nil
}
