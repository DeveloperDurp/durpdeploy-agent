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
	if err := pin.Validate(); err != nil {
		return nil, err
	}
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
	transport.Transport = stagingTransport{
		source: client.http.Transport, idleTimeout: 5 * time.Minute,
	}
	for attempt := 0; ; attempt++ {
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
		if err := client.promotePin(response.TLS); err != nil {
			return nil, errors.Join(err, response.Body.Close())
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
		if response.StatusCode != http.StatusOK {
			return nil, errors.Join(
				&StatusError{Status: response.StatusCode}, response.Body.Close(),
			)
		}
		file, receiveErr := client.receiveArtifact(response, pin)
		closeErr := response.Body.Close()
		if receiveErr != nil {
			return nil, errors.Join(receiveErr, closeErr)
		}
		if closeErr != nil {
			return nil, errors.Join(closeErr, file.Close(), os.Remove(file.Name()))
		}
		return file, nil
	}
}

func (client *Client) receiveArtifact(
	response *http.Response,
	pin artifact.Pin,
) (file *os.File, result error) {
	size, err := strconv.ParseInt(response.Header.Get("X-Artifact-Size"), 10, 64)
	if err != nil || size != pin.Size || response.ContentLength != pin.Size ||
		response.Header.Get("X-Artifact-SHA256") != pin.SHA256 {
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
		return file, err
	}
	if count != pin.Size || hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
		return file, artifact.ErrChecksum
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return file, err
	}
	return file, nil
}
