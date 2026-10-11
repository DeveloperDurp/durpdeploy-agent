//go:build linux

package agentclient

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStagingDoesNotRetryLocalWriteFailure(t *testing.T) {
	const probe = "DURPDEPLOY_TEST_STAGING_WRITE_FAILURE"
	if os.Getenv(probe) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0],
			"-test.run=^TestStagingDoesNotRetryLocalWriteFailure$")
		command.Env = append(os.Environ(), probe+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("local write failure probe: %v\n%s", err, output)
		}
		return
	}
	archive := stagingArchive(t)
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
			w.Header().
				Set("X-Artifact-SHA256", fmt.Sprintf("%x", sha256.Sum256(archive)))
			if _, err := w.Write(archive); err != nil {
				t.Error(err)
			}
		},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	waits := 0
	client.sleep = func(context.Context, time.Duration) error {
		waits++
		return context.Canceled
	}
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	signal.Ignore(syscall.SIGXFSZ)
	limit := original
	limit.Cur = 512
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := syscall.Setrlimit(
			syscall.RLIMIT_FSIZE,
			&original,
		); err != nil {
			t.Error(err)
		}
	}()
	file, err := client.Staging(
		t.Context(),
		agentproto.PollResponse{DeploymentID: 42},
		nil,
	)
	var writeError *os.PathError
	if file != nil || !errors.As(err, &writeError) ||
		writeError.Op != "write" ||
		waits != 0 {
		t.Fatalf(
			"local failure: file=%v error=%v retry waits=%d",
			file,
			err,
			waits,
		)
	}
}
