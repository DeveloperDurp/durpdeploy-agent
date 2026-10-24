package agentclient

import (
	"context"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func (client *Client) EnableContainers(
	ctx context.Context,
	config executor.ContainerConfig,
) error {
	return client.EnableContainersWithPolicy(ctx, config, client.ResourcePolicy().Container)
}

// EnableContainersWithPolicy captures container policy before polling starts.
func (client *Client) EnableContainersWithPolicy(ctx context.Context, config executor.ContainerConfig, policy resourcepolicy.Container) error {
	config.AgentID = client.agentID
	config.PairingIdentity = client.serverURL + "\x00" + client.identity.Fingerprint.String()
	limits := client.ResourcePolicy()
	limits.Container = policy
	runner, err := executor.NewContainerExecutorWithResourcePolicy(ctx, config, limits)
	if err != nil {
		return err
	}
	client.resourcePolicy = &limits
	client.container = runner
	client.protocol = agentproto.AgentV3
	return nil
}

func (client *Client) ContainerExecutor() *executor.ContainerExecutor { return client.container }

func (client *Client) SupportsStep(step executor.Step) bool {
	if client.protocol != agentproto.AgentV3 &&
		(step.ExecutionMode != "" || step.ContainerImage != "" || len(step.VariableNames) != 0) {
		return false
	}
	if err := step.ValidateExecution(); err != nil {
		return false
	}
	if err := step.ValidateLimits(client.ExecutionPolicy()); err != nil {
		return false
	}
	if step.ExecutionMode == agentproto.ExecutionContainer {
		if client.container == nil {
			return false
		}
		_, err := agentproto.ParseInterpreter(string(step.Interpreter))
		return err == nil
	}
	return client.SupportsInterpreter(step.Interpreter)
}

func (client *Client) ValidateExecutionReady(ctx context.Context) error {
	if client.container != nil {
		return client.container.Ready(ctx)
	}
	return nil
}
