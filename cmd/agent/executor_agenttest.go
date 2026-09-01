//go:build agenttest

package main

import runner "github.com/DeveloperDurp/durpdeploy-agent/executor"

func init() {
	newExecutor = runner.NewExecutorForAgentTest
}
