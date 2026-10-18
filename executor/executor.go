package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const (
	defaultStepTimeout = 5 * time.Minute
	serviceUsername    = "durpdeploy-agent"
)

var ErrCancelled = errors.New("step execution cancelled")

// ErrStepTimeout identifies an expired step deadline, including image preparation.
var ErrStepTimeout = errors.New("step execution timed out")

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
	hostSupervisor    string
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

func stepContextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.Join(ErrStepTimeout, err)
	}
	return err
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
	defer func() {
		if cleanupErr := os.RemoveAll(tmpDir); cleanupErr != nil {
			// A completed deployment must not be replayed because its private
			// script directory could not be removed. Block recovery and polling.
			err = errors.Join(err, ErrHostProcessCleanup, ErrContainerCleanup,
				fmt.Errorf("remove private script directory: %w", cleanupErr))
		}
	}()

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
	var supervision *hostCommand
	if job.stageDirectory != "" {
		supervision, err = e.supervisedCommand(stepCtx, cmd)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, supervision.close()) }()
		cmd = supervision.command
	} else {
		cmd.WaitDelay = 15 * time.Second
	}

	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, writer)
	cmd.Stderr = io.MultiWriter(&output, writer)
	host := ""
	if e.container != nil {
		e.container.mu.Lock()
		host = e.container.hostname
		e.container.mu.Unlock()
	}
	if err := writer.write(fmt.Sprintf(
		"Execution location: target=agent host=%q mode=host/shell interpreter=%q attempt=%d\n",
		writer.scrubber.Scrub(executionHostname(host)), job.interpreter, attempt,
	)); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return errors.Join(err, stepContextError(stepCtx), writer.flush())
	}
	pgid := cmd.Process.Pid
	if callbacks.trackProcessGroup != nil {
		callbacks.trackProcessGroup(pgid)
		if callbacks.untrackProcessGroup != nil {
			defer callbacks.untrackProcessGroup()
		}
	}
	err = cmd.Wait()
	if err != nil && stepCtx.Err() == context.DeadlineExceeded {
		err = errors.Join(err, stepContextError(stepCtx))
	}
	if supervision != nil {
		if cleanupErr := supervision.confirm(); cleanupErr != nil {
			return errors.Join(err, cleanupErr)
		}
	} else if err != nil && stepCtx.Err() != nil {
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
		message := fmt.Sprintf(
			"step %q: attempt %d failed: %v\n", job.name, attempt, err,
		)
		if stepCtx.Err() == context.DeadlineExceeded {
			message = fmt.Sprintf(
				"step %q: attempt %d timed out after %s\n",
				job.name,
				attempt,
				timeout,
			)
		}
		if writeErr := writer.write(message); writeErr != nil {
			return writeErr
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
