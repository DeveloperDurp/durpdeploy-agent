package agentclient

import (
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

// NewPairedWithSettings captures startup settings before polling begins.
func NewPairedWithSettings(
	stateDir string,
	version agentproto.AgentVersion,
	settings executor.StartupSettings,
) (*Client, error) {
	return NewPairedWithExecutionPolicy(stateDir, version, settings, resourcepolicy.Defaults().Execution)
}

// NewPairedWithExecutionPolicy captures settings and policy before polling begins.
func NewPairedWithExecutionPolicy(
	stateDir string,
	version agentproto.AgentVersion,
	settings executor.StartupSettings,
	policy resourcepolicy.Execution,
) (*Client, error) {
	limits := resourcepolicy.Defaults()
	limits.Execution = policy
	return NewPairedWithResourcePolicy(stateDir, version, settings, limits)
}

// NewPairedWithResourcePolicy captures a validated resource snapshot before polling.
func NewPairedWithResourcePolicy(
	stateDir string,
	version agentproto.AgentVersion,
	settings executor.StartupSettings,
	policy resourcepolicy.Limits,
) (*Client, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	client, err := NewPaired(stateDir, version)
	if err != nil {
		return nil, err
	}
	client.startupSettings = &settings
	client.resourcePolicy = &policy
	return client, nil
}

// ResourcePolicy returns a value snapshot. Legacy clients use the defaults.
func (client *Client) ResourcePolicy() resourcepolicy.Limits {
	if client.resourcePolicy == nil {
		return resourcepolicy.Defaults()
	}
	return *client.resourcePolicy
}

// ExecutionPolicy returns a value snapshot. Legacy clients use the default policy.
func (client *Client) ExecutionPolicy() resourcepolicy.Execution {
	return client.ResourcePolicy().Execution
}

// StartupSettings returns a copy. Legacy clients retain their default constructors.
func (client *Client) StartupSettings() (executor.StartupSettings, bool) {
	if client.startupSettings == nil {
		return executor.StartupSettings{}, false
	}
	return *client.startupSettings, true
}
