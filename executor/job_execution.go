package executor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
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
	if err := e.executionPolicy.ValidateStep(job.timeout, int64(job.maxRetries)); err != nil {
		return err
	}
	if job.timeout == 0 {
		job.timeout = time.Duration(e.executionPolicy.DefaultTimeoutSeconds) * time.Second
	}
	if err := (Step{ExecutionMode: job.executionMode, ContainerImage: job.containerImage, VariableNames: job.variableNames}).ValidateExecution(); err != nil {
		return err
	}
	if (job.executionMode == agentproto.ExecutionContainer && job.stageDirectory != "") ||
		(job.executionMode != agentproto.ExecutionContainer && job.stageVolume != "") ||
		(job.executionMode == agentproto.ExecutionContainer && job.artifactDirectory != "") ||
		(job.executionMode != agentproto.ExecutionContainer && job.artifactVolume != "") {
		return ErrInvalidStepExecution
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
	if err := job.setManagedPaths(); err != nil {
		return err
	}
	if _, err := agentproto.ParseInterpreter(
		string(normalizeInterpreter(job.interpreter)),
	); err != nil {
		return err
	}
	scrubber, err := e.scrubber(job.secrets)
	if err != nil {
		return err
	}
	return e.executeAttempts(ctx, job, callbacks, newRedactingWriter(scrubber, callbacks.writeLog))
}

func (e *Executor) executeAttempts(
	ctx context.Context,
	job Job,
	callbacks Callbacks,
	writer *redactingWriter,
) error {
	maxAttempts := job.maxRetries + 1
	var lastErr error
	var imageID string
	for index := range maxAttempts {
		attempt := index + 1
		if callbacks.cancelled != nil && callbacks.cancelled() {
			return ErrCancelled
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = e.executeAttempt(
			ctx,
			job,
			writer,
			callbacks,
			attempt,
			&imageID,
		)
		if lastErr == nil {
			return nil
		}
		if err := retryError(lastErr, callbacks); err != nil {
			return err
		}
		if attempt < maxAttempts {
			if err := writeRetry(writer, job, attempt); err != nil {
				return err
			}
		}
	}
	return lastErr
}

// allow: PARAMS_OK — preserve the host/container attempt contract and image pin.
func (e *Executor) executeAttempt(
	ctx context.Context,
	job Job,
	writer *redactingWriter,
	callbacks Callbacks,
	attempt int,
	imageID *string,
) error {
	if job.executionMode != agentproto.ExecutionContainer {
		return e.runAttempt(ctx, job, writer, callbacks, attempt)
	}
	if e.container == nil {
		return ErrContainerUnavailable
	}
	err := e.container.runAttempt(ctx, job, writer, callbacks, attempt, imageID, e.hostnameOverride())
	if err == nil {
		return nil
	}
	logErr := writer.write(fmt.Sprintf(
		"step %q: container attempt failed: %v\n", job.name, err,
	))
	return errors.Join(err, logErr, writer.flush())
}

func retryError(err error, callbacks Callbacks) error {
	if errors.Is(err, ErrContainerCleanup) || errors.Is(err, ErrLogDelivery) ||
		errors.Is(err, ErrStepOutputLimit) {
		return err
	}
	if callbacks.cancelled != nil && callbacks.cancelled() {
		return ErrCancelled
	}
	if errors.Is(err, ErrInterpreterUnavailable) ||
		errors.Is(err, ErrInvalidStepExecution) ||
		errors.Is(err, ErrContainerUnavailable) {
		return err
	}
	return nil
}

func writeRetry(writer *redactingWriter, job Job, attempt int) error {
	if err := writer.write(fmt.Sprintf(
		"step %q: retrying (attempt %d of %d)\n",
		job.name,
		attempt+1,
		job.maxRetries+1,
	)); err != nil {
		return err
	}
	return writer.flush()
}

func (job *Job) setManagedPaths() error {
	var err error
	if job.stageDirectory != "" {
		job.stageDirectory, err = filepath.Abs(job.stageDirectory)
		if err != nil {
			return fmt.Errorf("resolve host staging directory: %w",
				errors.Join(ErrInvalidStepExecution, err))
		}
		job.environment["DURPDEPLOY_STAGE_DIR"] = job.stageDirectory
	}
	if job.stageVolume != "" {
		job.environment["DURPDEPLOY_STAGE_DIR"] = "/stage"
	}
	if job.artifactDirectory != "" {
		job.artifactDirectory, err = filepath.Abs(job.artifactDirectory)
		if err != nil {
			return fmt.Errorf("resolve host package directory: %w",
				errors.Join(ErrInvalidStepExecution, err))
		}
		job.environment[artifact.PathVariable] = job.artifactDirectory
	}
	if job.artifactVolume != "" {
		job.environment[artifact.PathVariable] = "/artifacts"
	}
	return job.validateArtifactStorage()
}

func (job Job) validateArtifactStorage() error {
	if job.artifactVolume != "" && job.artifactVolume == job.stageVolume {
		return ErrInvalidStepExecution
	}
	if job.artifactDirectory == "" || job.stageDirectory == "" {
		return nil
	}
	packagePath, err := filepath.EvalSymlinks(job.artifactDirectory)
	if err != nil {
		return fmt.Errorf("resolve package storage: %w",
			errors.Join(ErrInvalidStepExecution, err))
	}
	stagePath, err := filepath.EvalSymlinks(job.stageDirectory)
	if err != nil {
		return fmt.Errorf("resolve staging storage: %w",
			errors.Join(ErrInvalidStepExecution, err))
	}
	separator := string(filepath.Separator)
	packagePath = strings.TrimSuffix(packagePath, separator) + separator
	stagePath = strings.TrimSuffix(stagePath, separator) + separator
	if strings.HasPrefix(packagePath, stagePath) || strings.HasPrefix(stagePath, packagePath) {
		return ErrInvalidStepExecution
	}
	return nil
}
