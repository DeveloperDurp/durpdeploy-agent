package executor

import (
	"errors"
	"os"

	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

// StartupSettings supplies captured application settings, separate from job data.
// Empty values retain their legacy meaning, including a missing service boundary.
type StartupSettings struct {
	ExecutionBoundary  string
	Hostname           string
	ExtraScrubPatterns string
}

func startupSettingsFromEnvironment() StartupSettings {
	return StartupSettings{
		ExecutionBoundary:  os.Getenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY"),
		Hostname:           os.Getenv("DURPDEPLOY_AGENT_HOSTNAME"),
		ExtraScrubPatterns: os.Getenv("DURPDEPLOY_EXTRA_SCRUB_PATTERNS"),
	}
}

// NewExecutorWithSettings captures settings without reading the environment.
// Execute rejects an invalid boundary or scrub policy before starting a job.
func NewExecutorWithSettings(settings StartupSettings, container *ContainerExecutor) *Executor {
	return NewExecutorWithExecutionPolicy(settings, resourcepolicy.Defaults().Execution, container)
}

// NewExecutorWithExecutionPolicy captures a retry and timeout policy.
// Execute rejects invalid settings or policy before starting a job.
func NewExecutorWithExecutionPolicy(settings StartupSettings, policy resourcepolicy.Execution, container *ContainerExecutor) *Executor {
	_, scrubErr := NewScrubberWithPatterns(nil, settings.ExtraScrubPatterns)
	boundaryErr := validateConfiguredExecutionBoundary(settings.ExecutionBoundary)
	return &Executor{
		boundaryValidated: true,
		sandboxErr:        errors.Join(boundaryErr, scrubErr, policy.Validate()),
		container:         container,
		startupSettings:   &settings,
		executionPolicy:   policy,
	}
}

func (e *Executor) hostnameOverride() string {
	if e.startupSettings == nil {
		return os.Getenv("DURPDEPLOY_AGENT_HOSTNAME")
	}
	return e.startupSettings.Hostname
}

func (e *Executor) scrubber(secrets []string) (*Scrubber, error) {
	if e.startupSettings == nil {
		return NewScrubber(secrets), nil
	}
	return NewScrubberWithPatterns(secrets, e.startupSettings.ExtraScrubPatterns)
}

func validateExecutionBoundary() error {
	return validateConfiguredExecutionBoundary(os.Getenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY"))
}
