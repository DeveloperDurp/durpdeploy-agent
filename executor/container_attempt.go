package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *ContainerExecutor) runAttempt(
	ctx context.Context,
	job Job,
	writer *redactingWriter,
	callbacks Callbacks,
	attempt int,
	imageID *string,
) (result error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cleanupErr != nil {
		return r.cleanupErr
	}
	timeout := job.timeout
	if timeout <= 0 {
		timeout = defaultStepTimeout
	}
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	writer.cancel = cancel
	if *imageID == "" {
		var err error
		*imageID, err = r.prepareImage(stepCtx, job.containerImage)
		if err != nil {
			if stepCtx.Err() != nil {
				return stepCtx.Err()
			}
			return err
		}
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("name attempt: %w", err)
	}
	name := "durpdeploy-agent-" + hex.EncodeToString(nonce)
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			10*time.Second,
		)
		defer cleanupCancel()
		if err := r.remove(cleanupCtx, name); err != nil {
			r.cleanupErr = err
			result = errors.Join(result, err)
		}
		result = errors.Join(result, writer.flush())
	}()
	args := []string{"run", "--interactive", "--pull=never", "--name=" + name,
		"--label=io.durpdeploy.agent=" + r.namespace,
		"--label=io.durpdeploy.attempt=" + name,
		"--network=none", "--read-only", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--user=65534:65534",
		"--tmpfs=/tmp:rw,nosuid,nodev,size=64m,mode=1777",
		"--env=HOME=/tmp", "--env=TERM=dumb", "--workdir=/tmp",
		"--pids-limit=128", "--memory=256m", "--cpus=1", "--log-driver=none"}
	if r.runtime == agentproto.RuntimePodman {
		args = append(
			args,
			"--image-volume=ignore",
			"--http-proxy=false",
			"--seccomp-policy=default",
			"--security-opt=seccomp="+podmanSeccompProfile,
		)
	} else {
		args = append(args, "--security-opt=seccomp=builtin")
	}
	names := make([]string, 0, len(job.environment))
	for name := range job.environment {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		args = append(args, "--env", name)
	}
	args = append(args, "--entrypoint="+string(job.interpreter), *imageID)
	switch job.interpreter {
	case InterpreterBash:
		args = append(args, "-s")
	case InterpreterPwsh:
		// stdin command mode evaluates statements separately. Stage the complete
		// script in the container's private tmpfs and preserve -File exit semantics.
		args = append(
			args,
			"-NoLogo",
			"-NoProfile",
			"-NonInteractive",
			"-Command",
			`$ErrorActionPreference = 'Stop'; [IO.File]::WriteAllText('/tmp/durpdeploy-step.ps1', [Console]::In.ReadToEnd()); & "$PSHOME/pwsh" -NoLogo -NoProfile -NonInteractive -File /tmp/durpdeploy-step.ps1; exit $LASTEXITCODE`,
		)
	case InterpreterPython3:
		args = append(args, "-")
	}
	cmd := r.command(stepCtx, args...)
	for _, name := range names {
		cmd.Env = append(cmd.Env, name+"="+job.environment[name])
	}
	cmd.Stdin = strings.NewReader(job.scriptBody)
	cmd.Stdout, cmd.Stderr = writer, writer
	setPgid(cmd)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start container client: %w", err)
	}
	if callbacks.trackProcessGroup != nil {
		callbacks.trackProcessGroup(cmd.Process.Pid)
		if callbacks.untrackProcessGroup != nil {
			defer callbacks.untrackProcessGroup()
		}
	}
	err := cmd.Wait()
	if stepCtx.Err() != nil {
		killProcessGroup(cmd.Process.Pid)
	}
	// A killed client does not prove a killed container; removal always runs above.
	if err != nil {
		if stepCtx.Err() != nil {
			return fmt.Errorf("container step interrupted: %w", stepCtx.Err())
		}
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
		return fmt.Errorf(
			"container interpreter %s failed or is missing in the image: %w",
			job.interpreter,
			err,
		)
	}
	return nil
}

// Resolve mutable tags once, reject image volumes on both engines, and run the
// inspected immutable image ID. A concurrent tag replacement cannot add mounts.
func (r *ContainerExecutor) prepareImage(
	ctx context.Context,
	image string,
) (string, error) {
	inspect := func() ([]byte, error) { return r.command(ctx, "image", "inspect", image).Output() }
	raw, err := inspect()
	if err != nil {
		if err := r.command(ctx, "pull", "--quiet", image).Run(); err != nil {
			return "", fmt.Errorf("pull image: %w", err)
		}
		raw, err = inspect()
		if err != nil {
			return "", fmt.Errorf("inspect pulled image: %w", err)
		}
	}
	var images []struct {
		ID     string
		Config struct{ Volumes map[string]json.RawMessage }
	}
	if err := json.Unmarshal(
		raw,
		&images,
	); err != nil || len(images) != 1 ||
		images[0].ID == "" {
		return "", ErrInvalidStepExecution
	}
	if len(images[0].Config.Volumes) > 0 {
		return "", fmt.Errorf(
			"image declares writable volumes: %w",
			ErrInvalidStepExecution,
		)
	}
	return images[0].ID, nil
}
