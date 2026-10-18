package main

import (
	"errors"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
)

const (
	maxResultDiagnosticBytes       = 256
	executionFailureDiagnostic     = "execution_failed: Deployment execution failed. See the redacted deployment logs."
	stepTimeoutDiagnostic          = "step_timeout: A deployment step exceeded its time limit."
	logDeliveryFailureDiagnostic   = "log_delivery_failed: Deployment logs could not be delivered."
	interruptedExecutionDiagnostic = "execution_interrupted: Deployment execution was interrupted. Managed staging must be cleaned before reporting the result."
)

// resultDiagnostic selects only fixed messages. Error strings, workload values,
// and claim authentication must never become part of a result diagnostic.
// Tests enforce the byte budget for the entire message catalogue.
func resultDiagnostic(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, runner.ErrLogDelivery):
		return logDeliveryFailureDiagnostic
	case errors.Is(err, runner.ErrStepTimeout):
		return stepTimeoutDiagnostic
	default:
		return executionFailureDiagnostic
	}
}
