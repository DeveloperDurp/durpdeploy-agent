package agentclient

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStagingRejectsOversizedLengthWithoutReadingBody(t *testing.T) {
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().
				Set("Content-Length", strconv.FormatInt(staging.MaxArchive+1, 10))
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	file, err := client.Staging(ctx, agentproto.PollResponse{
		DeploymentID: 42, ClaimToken: "claim-secret",
	}, nil)
	if file != nil {
		file.Close()
		t.Fatal("accepted an oversized download")
	}
	if !errors.Is(err, staging.ErrInvalid) {
		t.Fatalf(
			"did not reject the declared length before waiting for data: %v",
			err,
		)
	}
}
