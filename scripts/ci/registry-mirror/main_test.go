package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestServerBindsLoopbackAndServesRegistryPing(t *testing.T) {
	server := newServer()
	host, _, err := net.SplitHostPort(server.Addr)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("mirror must bind loopback, got %q", server.Addr)
	}
	if server.ReadHeaderTimeout <= 0 || server.ReadHeaderTimeout > 30*time.Second {
		t.Fatalf("unbounded header timeout: %s", server.ReadHeaderTimeout)
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(host, "0"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("server stopped with %v", err)
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Get("http://" + listener.Addr().String() + "/v2/")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != http.StatusOK || response.Header.Get("Docker-Distribution-API-Version") != "registry/2.0" {
		t.Fatalf("registry ping = %d %v", response.StatusCode, response.Header)
	}
}

func TestMirrorPreservesDigestAndUsesHarborScope(t *testing.T) {
	const digest = "sha256:ce64758a"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/service/token":
			if got := r.URL.Query().Get("scope"); got != "repository:dockerhub/library/alpine:pull" {
				t.Errorf("scope = %q", got)
			}
			if r.Header.Get("Authorization") != "" {
				t.Error("client credentials reached token endpoint")
			}
			_, _ = io.WriteString(w, `{"token":"anonymous-pull-token"}`)
		case "/v2/dockerhub/library/alpine/manifests/" + digest:
			if got := r.Header.Get("Authorization"); got != "Bearer anonymous-pull-token" {
				t.Errorf("authorization = %q", got)
			}
			w.Header().Set("Docker-Content-Digest", digest)
			_, _ = io.WriteString(w, "unchanged-manifest")
		default:
			t.Errorf("unexpected upstream request: %s", r.URL)
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	request := httptest.NewRequest(http.MethodGet, "/v2/library/alpine/manifests/"+digest, nil)
	request.Header.Set("Authorization", "Bearer must-not-forward")
	response := httptest.NewRecorder()
	mirror(target, upstream.Client()).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "unchanged-manifest" || response.Header().Get("Docker-Content-Digest") != digest {
		t.Fatalf("response = %d %q %v", response.Code, response.Body.String(), response.Header())
	}
}

func TestMirrorRejectsWritesAndMalformedRepositories(t *testing.T) {
	target, _ := url.Parse("https://harbor.invalid")
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{http.MethodPost, "/v2/library/alpine/blobs/uploads/", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v2/../private/manifests/latest", http.StatusBadRequest},
		{http.MethodGet, "/v2//manifests/latest", http.StatusBadRequest},
		{http.MethodGet, "/service/token", http.StatusNotFound},
		{http.MethodGet, "/v2/", http.StatusOK},
	} {
		t.Run(test.method+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			mirror(target, http.DefaultClient).ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
		})
	}
}

func TestMirrorFailsWhenHarborCannotAuthorize(t *testing.T) {
	for _, body := range []string{`{}`, `not-json`, strings.Repeat("x", (1<<20)+1)} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		target, _ := url.Parse(upstream.URL)
		response := httptest.NewRecorder()
		mirror(target, upstream.Client()).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/v2/library/alpine/manifests/latest", nil))
		upstream.Close()
		if response.Code != http.StatusBadGateway {
			t.Fatalf("status = %d", response.Code)
		}
	}
}
