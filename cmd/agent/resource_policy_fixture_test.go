package main

import (
	"net/http"
	"testing"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func withClaimPolicy(t *testing.T, policy resourcepolicy.Limits, next http.Handler) http.Handler {
	t.Helper()
	advertisement, err := policy.Advertisement()
	if err != nil {
		t.Fatal(err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == agentproto.PollPath {
			values := r.Header.Values(resourcepolicy.CapabilityHeader)
			if len(values) != 1 || policy.CheckCompatibility(values[0]) != nil {
				t.Error("incompatible claim fixture poll")
				w.WriteHeader(http.StatusConflict)
				return
			}
			w.Header().Set(resourcepolicy.CapabilityHeader, advertisement)
		}
		next.ServeHTTP(w, r)
	})
}
