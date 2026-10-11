package agentclient

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func TestStagingTransportKeepsProgressAndCancelsStalledBodies(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "progress", true: "stalled"}[stalled],
			func(t *testing.T) {
				// Given: transfer progress outlives several idle windows.
				identity := testIdentity(t)
				server := newTLSServer(
					t,
					identity,
					func(w http.ResponseWriter, r *http.Request) {
						for range 5 {
							if _, err := w.Write([]byte("data")); err != nil {
								return
							}
							w.(http.Flusher).Flush()
							if stalled {
								<-r.Context().Done()
								return
							}
							select {
							case <-time.After(40 * time.Millisecond):
							case <-r.Context().Done():
								return
							}
						}
					},
				)
				paired := testClient(
					t,
					server.URL,
					identity.Fingerprint.String(),
				)
				client := &http.Client{Transport: stagingTransport{
					source:      paired.http.Transport,
					idleTimeout: 100 * time.Millisecond,
				}}
				request, err := http.NewRequestWithContext(
					t.Context(),
					http.MethodGet,
					server.URL,
					nil,
				)
				if err != nil {
					t.Fatal(err)
				}
				// When
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				data, err := io.ReadAll(response.Body)
				// Then
				if stalled && err == nil {
					t.Fatal("stalled response did not cancel")
				}
				if !stalled &&
					(err != nil || string(data) != "datadatadatadatadata") {
					t.Fatalf("active response timed out: %q %v", data, err)
				}
			})
	}
}
