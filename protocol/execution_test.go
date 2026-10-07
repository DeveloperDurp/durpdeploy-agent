package agentproto

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestPollV3_validates_execution_capabilities(t *testing.T) {
	// Given
	valid := `{"protocol":"agent/3","agent_version":"test","supported_interpreters":["bash"],"execution_modes":["host","container"],"container_runtimes":["podman"],"container_interpreters":["bash","pwsh","python3"]}`
	tests := []struct {
		name, raw string
		reject    bool
	}{
		{"valid", valid, false},
		{
			"empty modes",
			strings.Replace(valid, `["host","container"]`, `[]`, 1),
			true,
		},
		{
			"unknown mode",
			strings.Replace(valid, `"host","container"`, `"host","remote"`, 1),
			true,
		},
		{
			"non-string mode",
			strings.Replace(valid, `"host","container"`, `"host",42`, 1),
			true,
		},
		{
			"non-string runtime",
			strings.Replace(valid, `["podman"]`, `[42]`, 1),
			true,
		},
		{
			"duplicate mode",
			strings.Replace(valid, `"host","container"`, `"host","host"`, 1),
			true,
		},
		{
			"unknown runtime",
			strings.Replace(valid, `"podman"`, `"tcp"`, 1),
			true,
		},
		{
			"duplicate runtime",
			strings.Replace(valid, `"podman"`, `"podman","podman"`, 1),
			true,
		},
		{
			"runtime without mode",
			strings.Replace(valid, `"host","container"`, `"host"`, 1),
			true,
		},
		{
			"missing runtime",
			strings.Replace(valid, `["podman"]`, `[]`, 1),
			true,
		},
		{
			"null interpreters",
			strings.Replace(
				valid,
				`"container_interpreters":["bash","pwsh","python3"]`,
				`"container_interpreters":null`,
				1,
			),
			true,
		},
		{
			"unknown interpreter",
			strings.Replace(valid, `"pwsh"`, `"sh"`, 1),
			true,
		},
		{
			"duplicate interpreter",
			strings.Replace(valid, `"pwsh"`, `"bash"`, 1),
			true,
		},
		{
			"legacy protocol",
			strings.Replace(valid, `agent/3`, `agent/2`, 1),
			true,
		},
		{
			"duplicate member",
			strings.Replace(
				valid,
				`"agent_version":"test"`,
				`"agent_version":"test","agent_version":"test"`,
				1,
			),
			true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// When
			_, err := DecodeRequest[PollRequest](strings.NewReader(test.raw))
			// Then
			if (err != nil) != test.reject {
				t.Fatalf("decode error = %v, reject = %t", err, test.reject)
			}
		})
	}
}

func TestPollV3_roundtrips_host_only_capabilities(t *testing.T) {
	// Given
	request := PollRequest{
		ProtocolEnvelope:      ProtocolEnvelope{Protocol: AgentV3},
		SupportedInterpreters: []Interpreter{InterpreterBash},
		ExecutionCapabilities: ExecutionCapabilities{
			ExecutionModes: []ExecutionMode{ExecutionHost},
		},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	// When
	decoded, err := DecodeRequest[PollRequest](strings.NewReader(string(raw)))
	// Then
	if err != nil || len(decoded.ContainerRuntimes) != 0 {
		t.Fatalf("roundtrip: %v, %v", decoded, err)
	}
}

func TestCleanupResult_requires_v3_and_blocks_replay(t *testing.T) {
	for _, version := range []ProtocolVersion{AgentV1, AgentV2, AgentV3} {
		t.Run(string(version), func(t *testing.T) {
			// Given
			raw := `{"protocol":"` + string(
				version,
			) + `","state":"cleanup_unconfirmed","claim_token":"token","error":"cleanup required"}`
			// When
			_, err := DecodeRequest[ResultRequest](strings.NewReader(raw))
			// Then
			if version == AgentV3 && err != nil {
				t.Fatal(err)
			}
			if version != AgentV3 && !errors.Is(err, ErrInvalidResultState) {
				t.Fatalf("legacy decode: %v", err)
			}
		})
	}
	// Given
	state, err := Complete(DispatchStarted, ResultCleanupUnconfirmed)
	if err != nil {
		t.Fatal(err)
	}
	// When
	_, err = Reclaim(state)
	// Then
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reclaim: %v", err)
	}
}
