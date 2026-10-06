package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const (
	defaultStepTimeout = 5 * time.Minute
	serviceUsername    = "durpdeploy-agent"
)

var ErrCancelled = errors.New("step execution cancelled")

func baseStepEnv() []string {
	environment := []string{
		"PATH=/usr/local/bin:/usr/local/sbin:/usr/bin:/usr/sbin:/bin:/sbin",
		"HOME=/nonexistent",
		"USER=" + serviceUsername,
		"LOGNAME=" + serviceUsername,
		"TERM=xterm",
	}
	if lang := os.Getenv("LANG"); lang != "" {
		environment = append(environment, "LANG="+lang)
	}
	return environment
}

// JobConfig supplies the values copied into an immutable Job.
type JobConfig struct {
	ExecutionMode  agentproto.ExecutionMode
	ContainerImage string
	VariableNames  []string
	DeploymentID   int64
	Name           string
	ScriptBody     string
	Interpreter    Interpreter
	Timeout        time.Duration
	MaxRetries     int
	Environment    map[string]string
	Secrets        []string
}

// Job is an immutable step execution request.
type Job struct {
	executionMode  agentproto.ExecutionMode
	containerImage string
	variableNames  []string
	deploymentID   int64
	name           string
	scriptBody     string
	interpreter    Interpreter
	timeout        time.Duration
	maxRetries     int
	environment    map[string]string
	secrets        []string
}

func NewJob(config JobConfig) Job {
	environment := make(map[string]string, len(config.Environment))
	for key, value := range config.Environment {
		environment[key] = value
	}
	return Job{
		executionMode:  config.ExecutionMode,
		containerImage: config.ContainerImage,
		variableNames:  slices.Clone(config.VariableNames),
		deploymentID:   config.DeploymentID,
		name:           config.Name,
		scriptBody:     config.ScriptBody,
		interpreter:    normalizeInterpreter(config.Interpreter),
		timeout:        config.Timeout,
		maxRetries:     config.MaxRetries,
		environment:    environment,
		secrets:        slices.Clone(config.Secrets),
	}
}

// CallbacksConfig supplies callbacks copied into immutable Callbacks.
type CallbacksConfig struct {
	WriteLog            func(string) error
	TrackProcessGroup   func(int)
	UntrackProcessGroup func()
	Cancelled           func() bool
}

// Callbacks connect execution to the caller's logs and lifecycle state.
type Callbacks struct {
	writeLog            func(string) error
	trackProcessGroup   func(int)
	untrackProcessGroup func()
	cancelled           func() bool
}

func NewCallbacks(config CallbacksConfig) Callbacks {
	return Callbacks{
		writeLog:            config.WriteLog,
		trackProcessGroup:   config.TrackProcessGroup,
		untrackProcessGroup: config.UntrackProcessGroup,
		cancelled:           config.Cancelled,
	}
}

// Executor executes jobs with the local sandbox and process isolation.
type Executor struct {
	boundaryValidated bool
	sandboxErr        error
	deadlineGrace     func()
	killGroup         func(int)
	container         *ContainerExecutor
}

// NewExecutorWithContainers keeps host execution inside its existing boundary.
func NewExecutorWithContainers(container *ContainerExecutor) *Executor {
	executor := NewExecutor()
	executor.container = container
	return executor
}

func NewExecutor() *Executor {
	return &Executor{
		boundaryValidated: true,
		sandboxErr:        validateExecutionBoundary(),
	}
}

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

func (e *Executor) runAttempt(
	runCtx context.Context,
	job Job,
	writer *redactingWriter,
	callbacks Callbacks,
	attempt int,
) (err error) {
	timeout := job.timeout
	if timeout <= 0 {
		timeout = defaultStepTimeout
	}
	stepCtx, cancel := context.WithTimeout(runCtx, timeout)
	defer cancel()
	writer.cancel = cancel

	tmpDir, err := os.MkdirTemp(
		"",
		fmt.Sprintf("durpdeploy-%d-*", job.deploymentID),
	)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	scriptPath := tmpDir + "/script" + scriptExtension(job.interpreter)
	if err := os.WriteFile(
		scriptPath,
		[]byte(job.scriptBody),
		0755,
	); err != nil {
		return err
	}

	executable, err := resolveInterpreter(job.interpreter)
	if err != nil {
		return err
	}
	cmd := e.command(stepCtx, tmpDir, executable, scriptPath)
	cmd.Env = baseStepEnv()
	for key, value := range job.environment {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
	}
	cmd.WaitDelay = 15 * time.Second

	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, writer)
	cmd.Stderr = io.MultiWriter(&output, writer)
	if err := cmd.Start(); err != nil {
		if err := writer.flush(); err != nil {
			return err
		}
		return err
	}
	pgid := cmd.Process.Pid
	if callbacks.trackProcessGroup != nil {
		callbacks.trackProcessGroup(pgid)
		if callbacks.untrackProcessGroup != nil {
			defer callbacks.untrackProcessGroup()
		}
	}
	err = cmd.Wait()
	if err != nil && stepCtx.Err() != nil {
		cleanupDone := make(chan struct{})
		go func(pgid int) {
			defer close(cleanupDone)
			e.killAfterDeadline(pgid)
		}(pgid)
		<-cleanupDone
	}
	if flushErr := writer.flush(); flushErr != nil {
		return flushErr
	}
	if err != nil {
		if stepCtx.Err() == context.DeadlineExceeded {
			if writeErr := writer.write(
				fmt.Sprintf(
					"step %q: attempt %d timed out after %s\n",
					job.name,
					attempt,
					timeout,
				),
			); writeErr != nil {
				return writeErr
			}
		} else {
			if writeErr := writer.write(
				fmt.Sprintf(
					"step %q: attempt %d failed: %v\n",
					job.name,
					attempt,
					err,
				),
			); writeErr != nil {
				return writeErr
			}
		}
		if flushErr := writer.flush(); flushErr != nil {
			return flushErr
		}
	}
	return err
}

func (e *Executor) command(
	ctx context.Context,
	tmpDir, executable, scriptPath string,
) *exec.Cmd {
	cmd := exec.CommandContext(ctx, executable, scriptPath)
	cmd.Dir = tmpDir
	setPgid(cmd)
	return cmd
}

func (e *Executor) killAfterDeadline(pgid int) {
	if e.deadlineGrace != nil {
		e.deadlineGrace()
	} else {
		time.Sleep(10 * time.Second)
	}
	if e.killGroup != nil {
		e.killGroup(pgid)
		return
	}
	killProcessGroup(pgid)
}
