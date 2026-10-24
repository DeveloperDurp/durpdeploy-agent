package agentclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// Artifact downloads only the package authenticated by this job's payload.
// The caller owns the returned private ZIP file and must close and remove it.
func (client *Client) Artifact(
	ctx context.Context,
	claim agentproto.PollResponse,
	pin artifact.Pin,
) (*os.File, error) {
	if err := pin.ValidateWithPolicy(client.ResourcePolicy().Artifact); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(client.ResourcePolicy().Artifact.FetchTimeoutSeconds)*time.Second)
	defer cancel()
	body, err := json.Marshal(artifact.DownloadRequest{ClaimToken: claim.ClaimToken})
	if err != nil {
		return nil, err
	}
	endpoint := client.serverURL + "/agent/v1/deployments/" +
		strconv.FormatInt(int64(claim.DeploymentID), 10) + "/artifact"
	transport := *client.http
	transport.Timeout = 0
	transport.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	source := client.artifactTransport()
	defer source.CloseIdleConnections()
	transport.Transport = source
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		request, err := http.NewRequestWithContext(
			ctx, http.MethodPost, endpoint, bytes.NewReader(body),
		)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := transport.Do(request)
		if err != nil {
			if err := client.wait(ctx, attempt, 0); err != nil {
				return nil, err
			}
			continue
		}
		if err := client.validateArtifactResponse(response); err != nil {
			return nil, err
		}
		if retryableStatus(response.StatusCode) {
			retryAfter := parseRetryAfter(
				response.Header.Get("Retry-After"), client.now(),
			)
			if err := response.Body.Close(); err != nil {
				return nil, err
			}
			if err := client.wait(ctx, attempt, retryAfter); err != nil {
				return nil, err
			}
			continue
		}
		file, retry, receiveErr := client.receiveArtifactResponse(
			ctx, response, pin, attempt,
		)
		if receiveErr != nil {
			return nil, receiveErr
		}
		if retry {
			continue
		}
		return file, nil
	}
}

// validateArtifactResponse permits only trusted success or retryable responses.
func (client *Client) validateArtifactResponse(response *http.Response) error {
	if err := client.promotePin(response.TLS); err != nil {
		return errors.Join(err, response.Body.Close())
	}
	if response.StatusCode != http.StatusOK && !retryableStatus(response.StatusCode) {
		return errors.Join(
			&StatusError{Status: response.StatusCode}, response.Body.Close(),
		)
	}
	return nil
}

// receiveArtifactResponse closes one response before returning a file or retry.
func (client *Client) receiveArtifactResponse(
	ctx context.Context,
	response *http.Response,
	pin artifact.Pin,
	attempt int,
) (*os.File, bool, error) {
	file, receiveErr := client.receiveArtifact(response, pin)
	closeErr := response.Body.Close()
	if receiveErr != nil {
		var readError *artifactReadError
		var localError *os.PathError
		if closeErr == nil && errors.As(receiveErr, &readError) &&
			!errors.As(receiveErr, &localError) {
			return nil, true, client.wait(ctx, attempt, 0)
		}
		return nil, false, errors.Join(receiveErr, closeErr)
	}
	if closeErr != nil {
		return nil, false, errors.Join(closeErr, file.Close(), os.Remove(file.Name()))
	}
	return file, false, nil
}

type artifactReadError struct{ error }

func (err *artifactReadError) Unwrap() error { return err.error }

func (client *Client) receiveArtifact(
	response *http.Response,
	pin artifact.Pin,
) (file *os.File, result error) {
	size, err := strconv.ParseInt(response.Header.Get("X-Artifact-Size"), 10, 64)
	if err != nil || size != pin.Size || response.ContentLength != pin.Size ||
		!strings.EqualFold(response.Header.Get("X-Artifact-SHA256"), pin.SHA256) {
		return nil, artifact.ErrChecksum
	}
	file, err = os.CreateTemp(filepath.Join(client.stateDir, "staging"), "package-*.zip")
	if err != nil {
		return nil, err
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, file.Close(), os.Remove(file.Name()))
			file = nil
		}
	}()
	hash := sha256.New()
	count, err := io.Copy(
		io.MultiWriter(file, hash), io.LimitReader(response.Body, pin.Size+1),
	)
	if err != nil {
		var writeError *os.PathError
		if errors.As(err, &writeError) {
			return file, err
		}
		return file, &artifactReadError{err}
	}
	if count != pin.Size || !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), pin.SHA256) {
		return file, artifact.ErrChecksum
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return file, err
	}
	return file, nil
}
