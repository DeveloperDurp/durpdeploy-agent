// Command registry-mirror routes the disposable CI Docker daemon's Docker Hub
// pulls through Harbor, including digest references embedded in contract code.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var repositoryPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*(?:/[a-z0-9]+(?:[._-][a-z0-9]+)*)*$`)

func main() {
	log.Fatal(newServer().ListenAndServe())
}

func newServer() *http.Server {
	target := &url.URL{Scheme: "https", Host: "harbor.durp.info"}
	return &http.Server{
		Addr:              "127.0.0.1:5000",
		Handler:           mirror(target, &http.Client{Timeout: 30 * time.Second}),
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func mirror(target *url.URL, client *http.Client) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = target.Host
		request.URL.Path = "/v2/dockerhub/" + strings.TrimPrefix(request.URL.Path, "/v2/")
		request.URL.RawPath = ""
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "pulls only", http.StatusMethodNotAllowed)
			return
		}
		if r.URL.Path == "/v2/" {
			w.Header().Set("Docker-Distribution-API-Version", "registry/2.0")
			w.WriteHeader(http.StatusOK)
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/v2/") {
			http.NotFound(w, r)
			return
		}
		repository := repositoryFromPath(r.URL.Path)
		if !repositoryPattern.MatchString(repository) {
			http.Error(w, "invalid image repository", http.StatusBadRequest)
			return
		}
		tokenURL := target.ResolveReference(&url.URL{Path: "/service/token"})
		tokenURL.RawQuery = url.Values{
			"service": {"harbor-registry"},
			"scope":   {"repository:dockerhub/" + repository + ":pull"},
		}.Encode()
		request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, tokenURL.String(), nil)
		if err != nil {
			http.Error(w, "token request failed", http.StatusBadGateway)
			return
		}
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "Harbor unavailable", http.StatusBadGateway)
			return
		}
		defer func() {
			// Closing this read-only response cannot change token validation or
			// the proxied response, which has already been sent at this point.
			_ = response.Body.Close()
		}()
		var token struct {
			Token string `json:"token"`
		}
		if response.StatusCode != http.StatusOK ||
			json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&token) != nil || token.Token == "" {
			http.Error(w, "Harbor token unavailable", http.StatusBadGateway)
			return
		}
		request = r.Clone(r.Context())
		request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.Token))
		proxy.ServeHTTP(w, request)
	})
}

func repositoryFromPath(path string) string {
	remainder := strings.TrimPrefix(path, "/v2/")
	for _, separator := range []string{"/manifests/", "/blobs/", "/tags/list"} {
		if before, _, ok := strings.Cut(remainder, separator); ok {
			return before
		}
	}
	return ""
}
