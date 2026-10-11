package executor

import (
	"context"
	"errors"
	"fmt"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (e *Executor) Execute(
	ctx context.Context,
	job Job,
	callbacks Callbacks,
) error {
	if !e.boundaryValidated {
		return fmt.Errorf("initialize runner sandbox: boundary not validated")
	}
	if e.sandboxErr != nil {
		return fmt.Errorf("initialize runner sandbox: %w", e.sandboxErr)
	}
	if err := (Step{ExecutionMode: job.executionMode, ContainerImage: job.containerImage, VariableNames: job.variableNames}).ValidateExecution(); err != nil {
		return err
	}
	environment, err := selectStepEnvironment(
		job.environment,
		job.variableNames,
		job.executionMode == agentproto.ExecutionContainer,
	)
	if err != nil {
		return err
	}
	job.environment = environment
	if job.stageDirectory != "" {
		job.environment["DURPDEPLOY_STAGE_DIR"] = job.stageDirectory
	}
	if job.stageVolume != "" {
		job.environment["DURPDEPLOY_STAGE_DIR"] = "/stage"
	}
	if _, err := agentproto.ParseInterpreter(
		string(normalizeInterpreter(job.interpreter)),
	); err != nil {
		return err
	}
	writer := newRedactingWriter(NewScrubber(job.secrets), callbacks.writeLog)
	maxAttempts := job.maxRetries + 1
	var lastErr error
	var imageID string
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if callbacks.cancelled != nil && callbacks.cancelled() {
			return ErrCancelled
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if job.executionMode == agentproto.ExecutionContainer {
			if e.container == nil {
				return ErrContainerUnavailable
			}
			lastErr = e.container.runAttempt(
				ctx,
				job,
				writer,
				callbacks,
				attempt,
				&imageID,
			)
			if lastErr != nil {
				if logErr := writer.write(
					fmt.Sprintf(
						"step %q: container attempt failed: %v\n",
						job.name,
						lastErr,
					),
				); logErr != nil {
					lastErr = errors.Join(lastErr, logErr)
				}
				if flushErr := writer.flush(); flushErr != nil {
					lastErr = errors.Join(lastErr, flushErr)
				}
			}
		} else {
			lastErr = e.runAttempt(ctx, job, writer, callbacks, attempt)
		}
		if lastErr == nil {
			return nil
		}
		if errors.Is(lastErr, ErrContainerCleanup) ||
			errors.Is(lastErr, ErrLogDelivery) ||
			errors.Is(lastErr, ErrStepOutputLimit) {
			return lastErr
		}
		if callbacks.cancelled != nil && callbacks.cancelled() {
			return ErrCancelled
		}
		if errors.Is(lastErr, ErrInterpreterUnavailable) ||
			errors.Is(lastErr, ErrInvalidStepExecution) ||
			errors.Is(lastErr, ErrContainerUnavailable) {
			return lastErr
		}
		if attempt < maxAttempts {
			if err := writer.write(
				fmt.Sprintf(
					"step %q: retrying (attempt %d of %d)\n",
					job.name,
					attempt+1,
					maxAttempts,
				),
			); err != nil {
				return err
			}
			if err := writer.flush(); err != nil {
				return err
			}
		}
	}
	return lastErr
}
