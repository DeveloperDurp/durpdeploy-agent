package agentclient

import (
	"context"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (client *Client) EnableContainers(
	ctx context.Context,
	config executor.ContainerConfig,
) error {
	config.AgentID = client.agentID
	config.PairingIdentity = client.serverURL + "\x00" + client.identity.Fingerprint.String()
	runner, err := executor.NewContainerExecutor(ctx, config)
	if err != nil {
		return err
	}
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
