//go:build linux && agenttest

package executor

import (
	"errors"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

// NewExecutorForAgentTest creates an unsandboxed executor for the agent's
// subprocess protocol tests. It is unavailable from ordinary builds.
func NewExecutorForAgentTest() *Executor {
	return &Executor{boundaryValidated: true, executionPolicy: resourcepolicy.Defaults().Execution}
}

// NewExecutorForAgentTestWithSettings captures settings for subprocess fixtures.
// Only the service-boundary check is bypassed in this nonproduction build.
func NewExecutorForAgentTestWithSettings(settings StartupSettings, container *ContainerExecutor) *Executor {
	return NewExecutorForAgentTestWithExecutionPolicy(settings, resourcepolicy.Defaults().Execution, container)
}

// NewExecutorForAgentTestWithExecutionPolicy bypasses only the service boundary.
func NewExecutorForAgentTestWithExecutionPolicy(settings StartupSettings, policy resourcepolicy.Execution, container *ContainerExecutor) *Executor {
	executor := NewExecutorWithExecutionPolicy(settings, policy, container)
	_, scrubErr := NewScrubberWithPatterns(nil, settings.ExtraScrubPatterns)
	executor.sandboxErr = errors.Join(scrubErr, policy.Validate())
	return executor
}
