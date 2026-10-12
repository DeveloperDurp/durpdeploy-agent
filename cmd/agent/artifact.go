package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type claimedPackage struct {
	claim agentproto.PollResponse
	pin   artifact.Pin
}

func (s *jobStaging) configureArtifact(
	ctx context.Context, client *agentclient.Client, pkg claimedPackage,
) (result error) {
	file, err := client.Artifact(ctx, pkg.claim, pkg.pin)
	if err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, file.Close(), os.Remove(file.Name()))
	}()
	s.artifactDirectory, err = artifact.ExtractZIP(ctx, file.Name())
	if err != nil {
		return err
	}
	s.artifactWorkspace, err = os.Open(s.artifactDirectory)
	if err != nil {
		return err
	}
	if s.container == nil {
		return makeArtifactReadOnly(s.artifactDirectory)
	}
	archive, err := os.CreateTemp(filepath.Dir(file.Name()), "package-*.tar")
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, archive.Close()) }()
	if err := os.Remove(archive.Name()); err != nil {
		return err
	}
	if err := artifact.WriteTar(ctx, s.artifactDirectory, archive); err != nil {
		return err
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		return err
	}
	s.artifactContainer, err = client.ContainerExecutor().NewStage(ctx, archive)
	return err
}

func makeArtifactReadOnly(directory string) error {
	return filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0500)
		}
		return os.Chmod(path, 0400)
	})
}
