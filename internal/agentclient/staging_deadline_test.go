package agentclient

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type stagingDeadlineTransport struct {
	t        *testing.T
	deadline time.Time
}

func (transport stagingDeadlineTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	deadline, ok := request.Context().Deadline()
	if !ok || !deadline.Equal(transport.deadline) {
		transport.t.Errorf(
			"transfer replaced the deployment deadline: %v",
			deadline,
		)
	}
	return nil, context.Canceled
}

func TestStagingPreservesLongDeploymentDeadline(t *testing.T) {
	// Given: a deployment allows enough time for a full archive on a slow link.
	identity := testIdentity(t)
	client := testClient(t, "https://127.0.0.1", identity.Fingerprint.String())
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Minute)
	defer cancel()
	deadline, _ := ctx.Deadline()
	client.http.Transport = stagingDeadlineTransport{t: t, deadline: deadline}
	client.sleep = func(context.Context, time.Duration) error { return context.Canceled }
	// When
	_, err := client.Staging(
		ctx,
		agentproto.PollResponse{DeploymentID: 42},
		nil,
	)
	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
