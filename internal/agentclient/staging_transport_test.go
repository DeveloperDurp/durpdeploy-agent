package agentclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"
)

func TestStagingTransportKeepsProgressAndCancelsStalledBodies(t *testing.T) {
	for _, stalled := range []bool{false, true} {
		t.Run(map[bool]string{false: "progress", true: "stalled"}[stalled],
			func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					// Virtual time separates the idle-window contract from TLS and
					// scheduler latency; the stream still honours request cancellation.
					client := &http.Client{Transport: stagingTransport{
						source:      stagingStreamTransport{stalled: stalled},
						idleTimeout: 100 * time.Millisecond,
					}}
					request, err := http.NewRequestWithContext(
						t.Context(),
						http.MethodGet,
						"https://example.invalid/staging",
						nil,
					)
					if err != nil {
						t.Fatal(err)
					}
					// When
					started := time.Now()
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					defer func() {
						if err := response.Body.Close(); err != nil {
							t.Error(err)
						}
					}()
					data, err := io.ReadAll(response.Body)
					// Then
					if stalled && (!errors.Is(err, context.Canceled) || string(data) != "data") {
						t.Fatalf("stalled response did not cancel after progress: %q %v", data, err)
					}
					if !stalled &&
						(err != nil || string(data) != "datadatadatadatadata") {
						t.Fatalf("active response timed out: %q %v", data, err)
					}
					if !stalled && time.Since(started) <= 100*time.Millisecond {
						t.Fatal("progress did not outlive the original idle window")
					}
				})
			})
	}
}

type stagingStreamTransport struct{ stalled bool }

func (transport stagingStreamTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	reader, writer := io.Pipe()
	stop := context.AfterFunc(request.Context(), func() {
		_ = writer.CloseWithError(request.Context().Err())
	})
	go func() {
		defer func() { _ = writer.Close() }()
		defer stop()
		for range 5 {
			select {
			case <-time.After(40 * time.Millisecond):
			case <-request.Context().Done():
				_ = writer.CloseWithError(request.Context().Err())
				return
			}
			if _, err := writer.Write([]byte("data")); err != nil {
				return
			}
			if transport.stalled {
				<-request.Context().Done()
				_ = writer.CloseWithError(request.Context().Err())
				return
			}
		}
	}()
	return &http.Response{StatusCode: http.StatusOK, Body: reader}, nil
}
