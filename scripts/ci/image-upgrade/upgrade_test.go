// The opt-in image integration test pairs the previous agent, upgrades its
// image, and checks authenticated polling and unchanged durable trust.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type runtime string

func (r runtime) command(ctx context.Context, arguments ...string) (string, error) {
	return command(ctx, string(r), arguments...)
}

func command(ctx context.Context, executable string, arguments ...string) (string, error) {
	output, err := exec.CommandContext(ctx, executable, arguments...).CombinedOutput()
	if err != nil {
		// Do not emit container logs: the unpaired agent prints its pairing code.
		return "", fmt.Errorf("%s %s: %w", executable, arguments[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

func TestUpgradeImages(t *testing.T) {
	previous := os.Getenv("AGENT_UPGRADE_PREVIOUS_IMAGE")
	candidate := os.Getenv("AGENT_UPGRADE_CANDIDATE_IMAGE")
	if previous == "" && candidate == "" {
		t.Skip("set both AGENT_UPGRADE_PREVIOUS_IMAGE and AGENT_UPGRADE_CANDIDATE_IMAGE")
	}
	runtimeName := os.Getenv("AGENT_TEST_RUNTIME")
	if runtimeName == "" {
		runtimeName = "docker"
	}
	parent, stopSignals := signal.NotifyContext(t.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()
	ctx, cancel := context.WithTimeout(parent, 3*time.Minute)
	defer cancel()
	if err := smoke(ctx, runtime(runtimeName), previous, candidate); err != nil {
		t.Fatal("image upgrade smoke:", err)
	}
	t.Log("linux/amd64 startup, pinned mTLS polling, graceful stop, and upgrade without re-pairing or trust changes")
}

func smoke(ctx context.Context, r runtime, previous, candidate string) (err error) {
	if (r != "docker" && r != "podman") || previous == "" || candidate == "" {
		return errors.New("require Docker/Podman and both image references")
	}
	versions, err := imageVersions(ctx, r, previous, candidate)
	if err != nil {
		return err
	}
	if versions[0] == versions[1] {
		return errors.New("upgrade must use distinct build identities")
	}
	identityDir, err := os.MkdirTemp("", "durpdeploy-upgrade-server-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(identityDir)) }()
	serverIdentity, err := agenttls.LoadOrCreate(identityDir, "https://127.0.0.1")
	if err != nil {
		return err
	}
	name := fmt.Sprintf("durpdeploy-upgrade-%d", time.Now().UnixNano())
	volume := name + "-state"
	if _, err := r.command(ctx, "volume", "create", volume); err != nil {
		return err
	}
	defer func() {
		// Cleanup has its own deadline, including after the smoke context expires.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, removeErr := r.command(ctx, "volume", "rm", "--force", volume)
		err = errors.Join(err, removeErr)
	}()
	address, err := freeAddress()
	if err != nil {
		return err
	}
	oldName := name + "-previous"
	defer func() { err = errors.Join(err, removeContainer(r, oldName)) }()
	if err := start(ctx, r, oldName, previous, volume, address); err != nil {
		return err
	}
	code, agentPin, err := pairingOffer(ctx, r, oldName)
	if err != nil {
		return err
	}
	polls := make(chan string, 16)
	server, endpoint, err := controlPlane(serverIdentity, agentPin, polls)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, server.Close()) }()
	if err := pair(ctx, "https://"+address, endpoint, code, agentPin, serverIdentity); err != nil {
		return err
	}
	if err := waitForPoll(ctx, polls, versions[0]); err != nil {
		return fmt.Errorf("previous agent did not poll after pairing: %w", err)
	}
	if err := stop(ctx, r, oldName); err != nil {
		return err
	}
	return upgrade(ctx, r, imageIdentity{candidate, versions[1]}, name+"-candidate", volume, address, polls)
}

func imageVersions(ctx context.Context, r runtime, images ...string) ([]string, error) {
	versions := make([]string, 0, len(images))
	for _, image := range images {
		platform, err := r.command(ctx, "image", "inspect", "--format", "{{.Os}}/{{.Architecture}}", image)
		if err != nil {
			return nil, err
		}
		if platform != "linux/amd64" {
			return nil, fmt.Errorf("image %s must be linux/amd64, got %s", image, platform)
		}
		version, err := r.command(ctx, "run", "--rm", "--read-only", "--cap-drop=ALL",
			"--security-opt=no-new-privileges", "--network=none", image, "version")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(version, "durpdeploy-agent ") {
			return nil, fmt.Errorf("image %s did not report an agent build identity", image)
		}
		versions = append(versions, strings.TrimPrefix(version, "durpdeploy-agent "))
	}
	return versions, nil
}

type imageIdentity struct{ reference, version string }

func upgrade(ctx context.Context, r runtime, candidate imageIdentity, name, volume, address string, polls <-chan string) (err error) {
	before, err := snapshot(ctx, r, candidate.reference, volume)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, removeContainer(r, name)) }()
	if err := start(ctx, r, name, candidate.reference, volume, address); err != nil {
		return err
	}
	if err := waitForPoll(ctx, polls, candidate.version); err != nil {
		return fmt.Errorf("upgraded agent did not resume pinned polling: %w", err)
	}
	if err := stop(ctx, r, name); err != nil {
		return err
	}
	after, err := snapshot(ctx, r, candidate.reference, volume)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("upgrade changed pairing state, certificate, key, or their permissions")
	}
	return nil
}

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	return listener.Addr().String(), listener.Close()
}

func start(ctx context.Context, r runtime, name, image, volume, address string) error {
	_, err := r.command(ctx, "run", "--detach", "--name", name, "--network=host",
		"--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
		"--memory=512m", "--cpus=1", "--pids-limit=128",
		"--tmpfs=/tmp:size=64m,mode=1777", "--volume", volume+":/var/lib/durpdeploy-agent",
		"--env", "DURPDEPLOY_AGENT_LISTEN_ADDR="+address, image)
	return err
}

func removeContainer(r runtime, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err := r.command(ctx, "rm", "--force", name)
	return err
}

func stop(ctx context.Context, r runtime, name string) error {
	if _, err := r.command(ctx, "stop", "--time=10", name); err != nil {
		return err
	}
	status, err := r.command(ctx, "inspect", "--format", "{{.State.ExitCode}}", name)
	if err != nil {
		return err
	}
	if status != "0" {
		return fmt.Errorf("agent did not stop gracefully: exit=%s", status)
	}
	return nil
}

func snapshot(ctx context.Context, r runtime, image, volume string) (string, error) {
	return r.command(ctx, "run", "--rm", "--read-only", "--network=none", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--volume", volume+":/var/lib/durpdeploy-agent:ro",
		"--entrypoint=/bin/sh", image, "-ec",
		"sha256sum identity.crt identity.key state.json; stat -c '%a %u %g' . identity.crt identity.key state.json")
}

func pairingOffer(ctx context.Context, r runtime, name string) (agentproto.PairingCode, agenttls.Fingerprint, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		logs, err := r.command(ctx, "logs", name)
		if err != nil {
			return agentproto.PairingCode{}, agenttls.Fingerprint{}, err
		}
		code, pin, ok := parsePairingOffer(logs)
		if ok {
			return code, pin, nil
		}
		select {
		case <-ctx.Done():
			return code, pin, ctx.Err()
		case <-ticker.C:
		}
	}
}

func parsePairingOffer(logs string) (agentproto.PairingCode, agenttls.Fingerprint, bool) {
	var code agentproto.PairingCode
	var pin agenttls.Fingerprint
	var haveCode, havePin bool
	for _, line := range strings.Split(logs, "\n") {
		if raw, ok := strings.CutPrefix(line, "Pairing code: "); ok {
			parsed, err := agentproto.ParsePairingCode(raw)
			code, haveCode = parsed, err == nil
		}
		if raw, ok := strings.CutPrefix(line, "Agent fingerprint: "); ok {
			parsed, err := agenttls.ParseFingerprint(raw)
			pin, havePin = parsed, err == nil
		}
	}
	return code, pin, haveCode && havePin
}

func controlPlane(identity agenttls.Identity, pin agenttls.Fingerprint, polls chan<- string) (*http.Server, string, error) {
	return controlPlaneAt(identity, pin, polls, "127.0.0.1:0")
}

func controlPlaneAt(identity agenttls.Identity, pin agenttls.Fingerprint, polls chan<- string, address string) (*http.Server, string, error) {
	config, err := agenttls.NewServerConfig(identity, "127.0.0.1", pin)
	if err != nil {
		return nil, "", err
	}
	listener, err := tls.Listen("tcp", address, config)
	if err != nil {
		return nil, "", err
	}
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != agentproto.PollPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		request, err := agentproto.DecodeRequest[agentproto.PollRequest](r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case polls <- string(request.AgentVersion):
		default:
		}
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Fprintln(os.Stderr, "upgrade control plane:", err)
		}
	}()
	return server, "https://" + listener.Addr().String(), nil
}

func pair(ctx context.Context, address, endpoint string, code agentproto.PairingCode, pin agenttls.Fingerprint, identity agenttls.Identity) error {
	agentPin, err := agentproto.ParseSHA256Pin(pin.String())
	if err != nil {
		return err
	}
	serverPin, err := agentproto.ParseSHA256Pin(identity.Fingerprint.String())
	if err != nil {
		return err
	}
	pull, err := agentproto.ParsePullEndpoint(endpoint)
	if err != nil {
		return err
	}
	body, err := json.Marshal(agentproto.PairRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{Protocol: agentproto.AgentV1},
		PairingCode:      code, AgentPin: agentPin, ServerPin: serverPin,
		PullEndpoint: pull, AgentID: "upgrade-smoke", CompletionAck: true,
	})
	if err != nil {
		return err
	}
	config, err := agenttls.NewPairingBootstrapClientConfig(address, pin)
	if err != nil {
		return err
	}
	config.Certificates = []tls.Certificate{identity.Certificate}
	transport := &http.Transport{TLSClientConfig: config}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+agentproto.ServerInitPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	closeErr := response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		return fmt.Errorf("pairing failed: HTTP %d", response.StatusCode)
	}
	return closeErr
}

func waitForPoll(ctx context.Context, polls <-chan string, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		select {
		case got := <-polls:
			if got == version {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
