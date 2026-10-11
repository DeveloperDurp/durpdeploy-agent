package agentclient

import (
	"context"
	"io"
	"net/http"
	"time"
)

// stagingTransport bounds stalled requests while active transfers keep running.
type stagingTransport struct {
	source      http.RoundTripper
	idleTimeout time.Duration
}

func (transport stagingTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	ctx, cancel := context.WithCancel(request.Context())
	idle := time.AfterFunc(transport.idleTimeout, cancel)
	stop := func() {
		idle.Stop()
		cancel()
	}
	progress := func() { idle.Reset(transport.idleTimeout) }
	request = request.Clone(ctx)
	if request.Body != nil {
		request.Body = &stagingProgressBody{
			ReadCloser: request.Body, progress: progress,
		}
	}
	response, err := transport.source.RoundTrip(request)
	if err != nil {
		stop()
		return nil, err
	}
	progress()
	response.Body = &stagingProgressBody{
		ReadCloser: response.Body, progress: progress, stop: stop,
	}
	return response, nil
}

type stagingProgressBody struct {
	io.ReadCloser
	progress func()
	stop     func()
}

func (body *stagingProgressBody) Read(data []byte) (int, error) {
	n, err := body.ReadCloser.Read(data)
	if n > 0 {
		body.progress()
	}
	return n, err
}

func (body *stagingProgressBody) Close() error {
	if body.stop != nil {
		defer body.stop()
	}
	return body.ReadCloser.Close()
}
