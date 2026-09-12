//go:build linux && agenttest

package executor

// NewExecutorForAgentTest creates an unsandboxed executor for the agent's
// subprocess protocol tests. It is unavailable from ordinary builds.
func NewExecutorForAgentTest() *Executor {
	return &Executor{boundaryValidated: true}
}
