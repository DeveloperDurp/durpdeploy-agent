package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

const stagingHelperImage = "docker.io/library/alpine@sha256:ce64758a109eb420d874a118f87920e625e12d3634e03b4a5573fd9f6e5d3507"
const quietFlag = "--quiet"

type ContainerStage struct {
	Volume string
	volume string
	keeper string
	runner *ContainerExecutor
}

// NewStage imports verified input into a bounded deployment-private volume.
func (r *ContainerExecutor) NewStage(
	ctx context.Context,
	source io.Reader,
) (stage *ContainerStage, result error) {
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	name := "durpdeploy-agent-stage-" + hex.EncodeToString(nonce[:])
	capacity := staging.MaxBytes + int64(
		staging.MaxFiles,
	)*int64(
		os.Getpagesize(),
	)
	stage = &ContainerStage{
		Volume: name, volume: name, keeper: name + "-keeper", runner: r,
	}
	r.mu.Lock()
	defer func() {
		r.mu.Unlock()
		if result != nil {
			result = errors.Join(
				result,
				stage.Close(context.WithoutCancel(ctx)),
			)
		}
	}()
	if _, err := r.command(ctx, "image", "inspect", stagingHelperImage).
		Output(); err != nil {
		if err := r.command(ctx, "pull", quietFlag, stagingHelperImage).
			Run(); err != nil {
			return stage, err
		}
	}
	args := []string{
		"volume",
		"create",
		"--label=io.durpdeploy.agentstage=" + r.namespace,
		"--driver=local",
		"--opt=type=tmpfs",
		"--opt=device=tmpfs",
		fmt.Sprintf(
			"--opt=o=noexec,nosuid,nodev,size=%d,nr_inodes=%d,uid=65534,gid=65534,mode=0755",
			capacity,
			// Allow security attributes in the tmpfs inode quota.
			2*(staging.MaxFiles+1),
		),
		name,
	}
	if err := r.command(ctx, args...).Run(); err != nil {
		return stage, err
	}
	mount := name + ":/stage:rw,nocopy"
	if r.runtime == agentproto.RuntimePodman {
		mount += ",noexec,nosuid,nodev,z"
	}
	args = []string{
		"run",
		"--detach",
		"--pull=never",
		"--name=" + stage.keeper,
		"--label=io.durpdeploy.agent=" + r.namespace,
		"--network=none",
		"--read-only",
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--user=65534:65534",
		"--pids-limit=32",
		fmt.Sprintf("--memory=%d", capacity+(128<<20)),
		"--cpus=1",
		"--volume=" + mount,
		"--entrypoint=/usr/bin/tail",
		stagingHelperImage,
		"-f",
		"/dev/null",
	}
	if err := r.command(ctx, args...).Run(); err != nil {
		return stage, err
	}
	command := r.command(
		ctx,
		"exec",
		"--interactive",
		stage.keeper,
		"tar",
		"-xf",
		"-",
		"-C",
		"/stage",
	)
	command.Stdin = source
	if err := command.Run(); err != nil {
		return stage, err
	}
	if r.stages == nil {
		r.stages = make(map[string]bool)
	}
	r.stages[name] = true
	return stage, nil
}

func (s *ContainerStage) Export(ctx context.Context, target io.Writer) error {
	s.runner.mu.Lock()
	defer s.runner.mu.Unlock()
	if !s.runner.stages[s.volume] {
		return ErrInvalidStepExecution
	}
	command := s.runner.command(
		ctx,
		"exec",
		s.keeper,
		"tar",
		"-cf",
		"-",
		"-C",
		"/stage",
		".",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	n, copyErr := io.Copy(target, io.LimitReader(stdout, (544<<20)+1))
	closeErr := stdout.Close()
	waitErr := command.Wait()
	if err := errors.Join(copyErr, closeErr, waitErr); err != nil {
		return err
	}
	if n > 544<<20 {
		return ErrInvalidStepExecution
	}
	return nil
}

func (s *ContainerStage) Close(ctx context.Context) error {
	s.runner.mu.Lock()
	defer s.runner.mu.Unlock()
	delete(s.runner.stages, s.volume)
	cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	err := s.runner.remove(cleanupCtx, s.keeper)
	volumes, listErr := s.runner.command(cleanupCtx, "volume", "ls", quietFlag, "--filter=label=io.durpdeploy.agentstage="+s.runner.namespace).
		Output()
	if listErr != nil {
		err = errors.Join(err, ErrContainerCleanup)
	}
	for _, name := range strings.Fields(string(volumes)) {
		if name != s.volume {
			continue
		}
		if s.runner.command(cleanupCtx, "volume", "rm", "--force", s.volume).
			Run() != nil {
			err = errors.Join(
				err,
				fmt.Errorf("remove staging volume: %w", ErrContainerCleanup),
			)
		}
	}
	if err != nil {
		s.runner.cleanupErr = err
	}
	return err
}

func (r *ContainerExecutor) cleanStagingVolumes(ctx context.Context) error {
	output, err := r.command(ctx, "volume", "ls", quietFlag, "--filter=label=io.durpdeploy.agentstage="+r.namespace).
		Output()
	if err != nil {
		return fmt.Errorf("list staging volumes: %w", ErrContainerCleanup)
	}
	for _, name := range strings.Fields(string(output)) {
		if err := r.command(ctx, "volume", "rm", "--force", name).
			Run(); err != nil {
			return fmt.Errorf(
				"remove orphaned staging volume: %w",
				ErrContainerCleanup,
			)
		}
	}
	return nil
}
