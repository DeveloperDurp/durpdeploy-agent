package agentclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Staging exchanges binary archives while heartbeats keep the claim live.
func (client *Client) Staging(
	ctx context.Context,
	claim agentproto.PollResponse,
	upload *os.File,
) (*os.File, error) {
	path := "download"
	if upload != nil {
		path = "upload"
	}
	endpoint := client.serverURL + "/agent/v1/deployments/" + strconv.FormatInt(
		int64(claim.DeploymentID),
		10,
	) + "/staging/" + path
	transport := *client.http
	transport.Timeout = 0
	transport.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	transport.Transport = stagingTransport{
		source: client.http.Transport, idleTimeout: 5 * time.Minute,
	}
	for attempt := 0; ; attempt++ {
		var body io.Reader
		var size int64
		if upload != nil {
			var err error
			body, size, err = stagingUploadBody(upload)
			if err != nil {
				return nil, err
			}
		}
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			endpoint,
			body,
		)
		if err != nil {
			return nil, err
		}
		request.ContentLength = size
		request.Header.Set("X-DurpDeploy-Claim", string(claim.ClaimToken))
		request.Header.Set("Content-Type", "application/x-tar")
		response, err := transport.Do(request)
		if err != nil {
			if err := client.wait(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}
		if err := client.promotePin(response.TLS); err != nil {
			return nil, errors.Join(err, response.Body.Close())
		}
		if retryableStatus(response.StatusCode) {
			retryAfter := parseRetryAfter(
				response.Header.Get("Retry-After"),
				client.now(),
			)
			closeErr := response.Body.Close()
			if closeErr != nil {
				return nil, closeErr
			}
			if err := client.wait(ctx, attempt, retryAfter); err != nil {
				return nil, err
			}
			continue
		}
		if upload != nil && response.StatusCode == 204 {
			return nil, response.Body.Close()
		}
		if upload != nil || response.StatusCode != 200 {
			return nil, errors.Join(
				&StatusError{Status: response.StatusCode},
				response.Body.Close(),
			)
		}
		file, err := os.CreateTemp(
			filepath.Join(client.stateDir, "staging"),
			"input-*.tar",
		)
		if err != nil {
			return nil, errors.Join(err, response.Body.Close())
		}
		if err := os.Remove(file.Name()); err != nil {
			return nil, errors.Join(err, file.Close(), response.Body.Close())
		}
		hash := sha256.New()
		n, copyErr := io.Copy(
			io.MultiWriter(file, hash),
			io.LimitReader(response.Body, staging.MaxArchive+1),
		)
		closeErr := response.Body.Close()
		var writeError *os.PathError
		if errors.As(copyErr, &writeError) && writeError.Op == "write" {
			return nil, errors.Join(copyErr, closeErr, file.Close())
		}
		if err := errors.Join(copyErr, closeErr); err != nil {
			if err := file.Close(); err != nil {
				return nil, err
			}
			if err := client.wait(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}
		if n > staging.MaxArchive || response.ContentLength != n ||
			hex.EncodeToString(
				hash.Sum(nil),
			) != response.Header.Get(
				"X-Artifact-SHA256",
			) {
			return nil, errors.Join(staging.ErrInvalid, file.Close())
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		if err := staging.Unpack(ctx, file, ""); err != nil {
			return nil, errors.Join(
				fmt.Errorf("validate staging input: %w", err),
				file.Close(),
			)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, errors.Join(err, file.Close())
		}
		return file, nil
	}
}

func stagingUploadBody(upload *os.File) (io.Reader, int64, error) {
	info, err := upload.Stat()
	if err != nil {
		return nil, 0, err
	}
	size := info.Size()
	if size > staging.MaxArchive {
		return nil, 0, staging.ErrInvalid
	}
	if _, err := upload.Seek(0, io.SeekStart); err != nil {
		return nil, 0, err
	}
	return io.NewSectionReader(upload, 0, size), size, nil
}
