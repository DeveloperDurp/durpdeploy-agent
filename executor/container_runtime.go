package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

var (
	ErrContainerUnavailable = errors.New("container runtime is unavailable")
	ErrContainerCleanup     = errors.New("container cleanup unconfirmed")
)

const podmanSeccompProfile = "/usr/share/containers/seccomp.json"

type ContainerConfig struct {
	Runtime   agentproto.ContainerRuntime
	SocketURL string
	AgentID   agentproto.AgentID
}

// ContainerExecutor owns attempts in one agent identity's runtime namespace.
// Construct it only for explicit container opt-in. Socket access is never granted
// or repaired here; the operator must supply it.
type ContainerExecutor struct {
	runtime    agentproto.ContainerRuntime
	binary     string
	socketURL  string
	socketPath string
	namespace  string
	clientHome string
	mu         sync.Mutex
	cleanupErr error
}

func NewContainerExecutor(
	ctx context.Context,
	config ContainerConfig,
) (*ContainerExecutor, error) {
	if _, err := agentproto.ParseContainerRuntime(
		string(config.Runtime),
	); err != nil {
		return nil, err
	}
	u, err := url.Parse(config.SocketURL)
	if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil ||
		u.RawQuery != "" ||
		u.Fragment != "" ||
		u.Opaque != "" ||
		!filepath.IsAbs(u.Path) ||
		config.AgentID == "" {
		return nil, fmt.Errorf(
			"require an absolute local Unix socket URL and paired agent identity: %w",
			ErrContainerUnavailable,
		)
	}
	binary, err := exec.LookPath(string(config.Runtime))
	if err != nil {
		return nil, fmt.Errorf(
			"install the selected runtime client: %w",
			ErrContainerUnavailable,
		)
	}
	digest := sha256.Sum256([]byte(config.AgentID))
	runner := &ContainerExecutor{
		runtime:    config.Runtime,
		binary:     binary,
		socketURL:  config.SocketURL,
		socketPath: u.Path,
		namespace:  "agent-" + hex.EncodeToString(digest[:]),
	}
	runner.clientHome, err = os.MkdirTemp("", "durpdeploy-runtime-client-*")
	if err != nil {
		return nil, fmt.Errorf("create private runtime client home: %w", err)
	}
	if err := os.Mkdir(
		filepath.Join(runner.clientHome, ".config"),
		0700,
	); err != nil {
		return nil, errors.Join(err, runner.Close())
	}
	if err := runner.Ready(ctx); err != nil {
		return nil, errors.Join(err, runner.Close())
	}
	return runner, nil
}

func (r *ContainerExecutor) Runtime() agentproto.ContainerRuntime { return r.runtime }

// Close removes only the temporary client configuration directory created here.
func (r *ContainerExecutor) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return os.RemoveAll(r.clientHome)
}

func (r *ContainerExecutor) command(
	ctx context.Context,
	args ...string,
) *exec.Cmd {
	clientArgs := []string{
		"--host=" + r.socketURL,
		"--config=" + filepath.Join(r.clientHome, ".docker"),
	}
	if r.runtime == agentproto.RuntimePodman {
		clientArgs = []string{"--remote", "--url=" + r.socketURL}
	}
	cmd := exec.CommandContext(ctx, r.binary, append(clientArgs, args...)...)
	// A minimal client environment excludes inherited contexts, endpoints, auth
	// overrides and every deployment variable. Only run commands add selected names.
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + r.clientHome,
		"TERM=dumb",
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

// Ready validates the runtime and confirms removal of this agent's orphaned
// attempts. A failed check stops polling; a later successful check allows recovery.
func (r *ContainerExecutor) Ready(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := os.Stat(r.socketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf(
			"check configured runtime socket and service access: %w",
			ErrContainerUnavailable,
		)
	}
	raw, err := r.command(checkCtx, "info", "--format", "{{json .}}").Output()
	if err != nil {
		return fmt.Errorf(
			"check runtime socket access: %w",
			ErrContainerUnavailable,
		)
	}
	if err := r.validateRuntimeInfo(raw); err != nil {
		return err
	}
	ids, err := r.ownedContainers(checkCtx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := r.remove(checkCtx, id); err != nil {
			r.cleanupErr = err
			return err
		}
	}
	remaining, err := r.ownedContainers(checkCtx)
	if err != nil || len(remaining) != 0 {
		r.cleanupErr = fmt.Errorf(
			"verify orphan cleanup before polling: %w",
			ErrContainerCleanup,
		)
		return r.cleanupErr
	}
	r.cleanupErr = nil
	return nil
}

func (r *ContainerExecutor) validateRuntimeInfo(raw []byte) error {
	var info struct {
		OSType      string
		MemoryLimit bool
		CpuCfsQuota bool
		PidsLimit   bool
		Host        struct {
			OS                string
			CgroupControllers []string
			Security          struct {
				SeccompEnabled     bool
				SeccompProfilePath string
			}
		}
		SecurityOptions []string
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return fmt.Errorf(
			"decode runtime preflight: %w",
			ErrContainerUnavailable,
		)
	}
	ready := info.OSType == "linux" && info.MemoryLimit && info.CpuCfsQuota &&
		info.PidsLimit
	seccomp := slices.Contains(
		info.SecurityOptions,
		"name=seccomp,profile=builtin",
	)
	if r.runtime == agentproto.RuntimePodman {
		ready = info.Host.OS == "linux"
		for _, controller := range []string{"cpu", "memory", "pids"} {
			ready = ready &&
				slices.Contains(info.Host.CgroupControllers, controller)
		}
		seccomp = info.Host.Security.SeccompEnabled &&
			info.Host.Security.SeccompProfilePath == podmanSeccompProfile
	}
	if !ready || !seccomp {
		return fmt.Errorf(
			"runtime requires Linux, seccomp and CPU/memory/PID cgroup limits: %w",
			ErrContainerUnavailable,
		)
	}
	return nil
}

func (r *ContainerExecutor) ownedContainers(
	ctx context.Context,
) ([]string, error) {
	raw, err := r.command(ctx, "ps", "--all", "--quiet", "--filter=label=io.durpdeploy.agent="+r.namespace).
		Output()
	if err != nil {
		return nil, fmt.Errorf("list owned attempts: %w", ErrContainerCleanup)
	}
	return strings.Fields(string(raw)), nil
}

func (r *ContainerExecutor) remove(ctx context.Context, name string) error {
	args := []string{"rm", "--force", "--volumes", name}
	if r.runtime == agentproto.RuntimePodman {
		args = []string{
			"rm",
			"--force",
			"--volumes",
			"--time=0",
			"--ignore",
			name,
		}
	}
	// A missing container is accepted only after an independent list confirms it.
	removeErr := r.command(ctx, args...).Run()
	raw, err := r.command(ctx, "ps", "--all", "--quiet", "--filter=name="+name).
		Output()
	if err != nil || strings.TrimSpace(string(raw)) != "" {
		return fmt.Errorf(
			"remove attempt and confirm absence: %w",
			ErrContainerCleanup,
		)
	}
	if removeErr != nil && ctx.Err() != nil {
		return fmt.Errorf("cleanup deadline expired: %w", ErrContainerCleanup)
	}
	return nil
}
