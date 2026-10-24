package resourcepolicy

import (
	"errors"
	"fmt"
	"reflect"
	"time"
)

var ErrExecutionLimits = errors.New("invalid step execution limits")

// Validate checks an execution policy before duration and retry conversions.
func (p Execution) Validate() error {
	if err := validateNumbers(reflect.ValueOf(p), "limits.execution"); err != nil {
		return err
	}
	if p.DefaultTimeoutSeconds > p.MaxTimeoutSeconds {
		return errors.New("default timeout exceeds maximum timeout")
	}
	return nil
}

// ValidateStep permits zero timeout for the configured default and zero retries.
// Fractional positive durations remain supported by the executor Go interface.
func (p Execution) ValidateStep(timeout time.Duration, maxRetries int64) error {
	if err := p.Validate(); err != nil {
		return err
	}
	switch {
	case timeout < 0:
		return fmt.Errorf("%w: timeout must be non-negative", ErrExecutionLimits)
	case timeout > time.Duration(p.MaxTimeoutSeconds)*time.Second:
		return fmt.Errorf("%w: timeout must not exceed %d seconds", ErrExecutionLimits, p.MaxTimeoutSeconds)
	case maxRetries < 0:
		return fmt.Errorf("%w: max retries must be non-negative", ErrExecutionLimits)
	case maxRetries > p.MaxRetries:
		return fmt.Errorf("%w: max retries must not exceed %d", ErrExecutionLimits, p.MaxRetries)
	default:
		return nil
	}
}
