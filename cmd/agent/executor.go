package main

import (
	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
)

func executorForClient(client *agentclient.Client) *runner.Executor {
	if settings, configured := client.StartupSettings(); configured {
		return runner.NewExecutorWithExecutionPolicy(settings, client.ExecutionPolicy(), client.ContainerExecutor())
	}
	if client.ContainerExecutor() != nil {
		return runner.NewExecutorWithContainers(client.ContainerExecutor())
	}
	return runner.NewExecutor()
}
