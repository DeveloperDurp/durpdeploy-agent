package agentclient

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestStagingRejectsRedirectsBeforeForwardingCredentials(t *testing.T) {
	for _, upload := range []bool{false, true} {
		t.Run(map[bool]string{false: "download", true: "upload"}[upload],
			func(t *testing.T) {
				var forwarded atomic.Int32
				target := httptest.NewServer(http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) {
						forwarded.Add(1)
						w.WriteHeader(http.StatusNoContent)
					},
				))
				defer target.Close()
				identity := testIdentity(t)
				server := newTLSServer(t, identity,
					func(w http.ResponseWriter, r *http.Request) {
						http.Redirect(w, r, target.URL, http.StatusSeeOther)
					})
				client := testClient(t, server.URL,
					identity.Fingerprint.String())
				var source *os.File
				if upload {
					var err error
					source, err = os.CreateTemp(t.TempDir(), "archive")
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if err := source.Close(); err != nil {
							t.Error(err)
						}
					})
				}
				_, err := client.Staging(t.Context(), agentproto.PollResponse{
					DeploymentID: 42, ClaimToken: "claim-secret",
				}, source)
				var status *StatusError
				if !errors.As(err, &status) ||
					status.Status != http.StatusSeeOther || forwarded.Load() != 0 {
					t.Fatalf("redirect err=%v forwarded=%d", err, forwarded.Load())
				}
			})
	}
}
