package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

const hostSupervisorArgument = "--durpdeploy-host-supervisor"
const hostSupervisorStopped = "durpdeploy-host-stopped/1\n"
const hostCleanupTimeout = 30 * time.Second

// ErrHostProcessCleanup requires a service restart before staging recovery.
var ErrHostProcessCleanup = errors.New("host process cleanup unconfirmed")

// EnableHostSupervisor enables staged host execution in binaries that call
// HostSupervisorMain before their normal entry point.
func (e *Executor) EnableHostSupervisor() error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate host supervisor: %w", err)
	}
	e.hostSupervisor = executable
	return nil
}

type hostCommand struct {
	command *exec.Cmd
	report  *os.File
	ack     *os.File
	stop    *os.File
	control *os.File
}

func (e *Executor) supervisedCommand(
	ctx context.Context,
	original *exec.Cmd,
) (*hostCommand, error) {
	if e.hostSupervisor == "" {
		return nil, fmt.Errorf(
			"enable a host supervisor for staged execution: %w",
			ErrInvalidStepExecution,
		)
	}
	report, ack, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create supervisor report pipe: %w", err)
	}
	stop, control, err := os.Pipe()
	if err != nil {
		return nil, errors.Join(err, report.Close(), ack.Close())
	}
	command := exec.CommandContext(ctx, e.hostSupervisor,
		hostSupervisorArgument, original.Args[0], original.Args[1])
	command.Dir, command.Env = original.Dir, original.Env
	command.ExtraFiles = []*os.File{ack, stop}
	command.WaitDelay = hostCleanupTimeout + 5*time.Second
	command.Cancel = func() error {
		if err := control.Close(); errors.Is(err, os.ErrClosed) {
			return os.ErrProcessDone
		} else {
			return err
		}
	}
	setPgid(command)
	return &hostCommand{command, report, ack, stop, control}, nil
}

func (s *hostCommand) confirm() error {
	if err := s.ack.Close(); err != nil {
		return errors.Join(ErrHostProcessCleanup, ErrContainerCleanup, err)
	}
	data, err := io.ReadAll(io.LimitReader(s.report, 64))
	if err != nil || string(data) != hostSupervisorStopped {
		return errors.Join(ErrHostProcessCleanup, ErrContainerCleanup, err)
	}
	return nil
}

func (s *hostCommand) close() error {
	var result error
	for _, file := range []*os.File{s.report, s.ack, s.stop, s.control} {
		if err := file.Close(); !errors.Is(err, os.ErrClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}
