package agentclient

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
	"github.com/DeveloperDurp/durpdeploy-agent/transport"
)

const legacyAgentVersion = agentproto.AgentVersion("legacy-unknown")

// NewPaired restores the identity and exact server pins established by pairing.
func NewPaired(
	stateDir string,
	agentVersion agentproto.AgentVersion,
) (*Client, error) {
	state, err := agentstate.NewStore(stateDir).Load()
	if err != nil {
		return nil, err
	}
	if agentVersion == "" {
		agentVersion = agentproto.AgentVersion(state.AgentVersion)
	}
	if agentVersion == "" {
		agentVersion = legacyAgentVersion
	}
	identity, err := agenttls.LoadExisting(stateDir)
	if err != nil {
		return nil, fmt.Errorf("load paired agent identity: %w", err)
	}
	client := &Client{
		serverURL:    state.ServerURL,
		stateDir:     stateDir,
		agentID:      agentproto.AgentID(state.AgentID),
		agentVersion: agentVersion,
		protocol:     agentproto.AgentV2,
		interpreters: executor.SupportedInterpreters(),
		identity:     identity,
		pins:         append([]agenttls.Fingerprint(nil), state.ServerPins...),
		state:        state,
		now:          time.Now,
		sleep:        sleep,
		jitter:       randomInt,
	}
	if err := client.ResetStaging(); err != nil {
		return nil, err
	}
	tlsConfig, err := client.tlsConfig()
	if err != nil {
		return nil, err
	}
	client.http = &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			MaxConnsPerHost:     2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	return client, nil
}

// ResetStaging purges orphaned host files before recovery or polling resumes.
func (client *Client) ResetStaging() error {
	stageDir := filepath.Join(client.stateDir, "staging")
	if err := staging.Purge(stageDir); err != nil {
		return fmt.Errorf("purge orphaned staging: %w", err)
	}
	return os.Mkdir(stageDir, 0700)
}
