//go:build agenttest

package main

import (
	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
)

func init() {
	newExecutor = func(client *agentclient.Client) *runner.Executor {
		if settings, configured := client.StartupSettings(); configured {
			return runner.NewExecutorForAgentTestWithExecutionPolicy(settings, client.ExecutionPolicy(), client.ContainerExecutor())
		}
		if client.ContainerExecutor() != nil {
			return runner.NewExecutorWithContainers(client.ContainerExecutor())
		}
		return runner.NewExecutorForAgentTest()
	}
}
