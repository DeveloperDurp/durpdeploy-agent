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

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestTransfersDoNotRetryLocalWriteFailure(t *testing.T) {
	const probe = "DURPDEPLOY_TEST_STAGING_WRITE_FAILURE"
	if os.Getenv(probe) == "" {
		for _, mode := range []string{"staging", "artifact"} {
			t.Run(mode, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, os.Args[0],
					"-test.run=^TestTransfersDoNotRetryLocalWriteFailure$")
				command.Env = append(os.Environ(), probe+"="+mode)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("local write failure probe: %v\n%s", err, output)
				}
			})
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
			w.Header().Set("X-Artifact-Size", strconv.Itoa(len(archive)))
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
	var file *os.File
	var err error
	claim := agentproto.PollResponse{DeploymentID: 42}
	if os.Getenv(probe) == "artifact" {
		file, err = client.Artifact(t.Context(), claim, artifact.Pin{
			SHA256: fmt.Sprintf("%x", sha256.Sum256(archive)),
			Size:   int64(len(archive)),
		})
	} else {
		file, err = client.Staging(t.Context(), claim, nil)
	}
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
