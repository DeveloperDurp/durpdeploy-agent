package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type jobStaging struct {
	directory string
	parent    string
	original  os.FileInfo
	root      *os.Root
	container *runner.ContainerStage
	input     *os.File
}

func newJobStaging(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) (*jobStaging, error) {
	input, err := client.Staging(ctx, claim, nil)
	if err != nil {
		return nil, err
	}
	parent, err := os.MkdirTemp(
		filepath.Join(client.StateDir(), "staging"),
		"claim-",
	)
	if err != nil {
		return nil, errors.Join(err, input.Close())
	}
	stage := &jobStaging{
		directory: filepath.Join(parent, "workspace"),
		parent:    parent, input: input,
	}
	stage.root, err = os.OpenRoot(filepath.Join(client.StateDir(), "staging"))
	if err != nil {
		return stage, err
	}
	if err := os.Mkdir(stage.directory, 0700); err != nil {
		return stage, err
	}
	stage.original, err = os.Lstat(stage.directory)
	return stage, err
}

func (s *jobStaging) configure(
	ctx context.Context,
	executor *runner.ContainerExecutor,
	config *runner.ExecutionConfig,
) error {
	if config.Steps[0].ExecutionMode == agentproto.ExecutionContainer {
		container, err := executor.NewStage(ctx, s.input)
		s.container = container
		if err != nil {
			return err
		}
		config.StageVolume = container.Volume
		return nil
	}
	if err := staging.Unpack(ctx, s.input, s.directory); err != nil {
		return err
	}
	config.StageDirectory = s.directory
	return nil
}

func (s *jobStaging) publish(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) (result error) {
	file, err := os.CreateTemp(filepath.Dir(s.directory), "output-*.tar")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, file.Close()) }()
	if err := os.Remove(file.Name()); err != nil {
		return err
	}
	if s.container != nil {
		if err := s.container.Export(ctx, file); err != nil {
			return err
		}
	} else {
		current, err := os.Lstat(s.directory)
		if err != nil {
			return err
		}
		if !os.SameFile(s.original, current) || !current.IsDir() {
			return staging.ErrInvalid
		}
		if err := staging.Pack(ctx, s.directory, file); err != nil {
			return err
		}
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := staging.Unpack(ctx, file, ""); err != nil {
		return err
	}
	_, err = client.Staging(ctx, claim, file)
	return err
}

func (s *jobStaging) close(
	ctx context.Context,
	client *agentclient.Client,
) error {
	var result error
	if s.container != nil {
		result = s.container.Close(ctx)
	}
	// The handle retains the owned tree when a script renames its root.
	if s.root != nil {
		if err := staging.PurgeRoot(s.root); err != nil {
			result = errors.Join(result, runner.ErrContainerCleanup, err)
		}
		result = errors.Join(result, s.root.Close())
	}
	// Claims execute serially, so purge replacement trees before polling.
	if err := client.ResetStaging(); err != nil {
		result = errors.Join(result, runner.ErrContainerCleanup, err)
	}
	return errors.Join(result, s.input.Close())
}
