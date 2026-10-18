package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"testing"
	"time"

	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPairAndControlPlaneRequirePinnedIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	directory := t.TempDir()
	bootstrap, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: directory, ListenAddr: "127.0.0.1:0", AgentVersion: "previous",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := bootstrap.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	agentPin, err := agenttls.ParseFingerprint(bootstrap.Offer().AgentPin.String())
	if err != nil {
		t.Fatal(err)
	}
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	polls := make(chan string, 16)
	server, endpoint, err := controlPlane(identity, agentPin, polls)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := pair(ctx, bootstrap.Endpoint(), endpoint, bootstrap.Offer().Code, agenttls.Fingerprint{}, identity); err == nil {
		t.Fatal("pairing accepted an unpinned bootstrap agent")
	}
	if err := pair(ctx, bootstrap.Endpoint(), endpoint, bootstrap.Offer().Code, agentPin, identity); err != nil {
		t.Fatal(err)
	}
	state, err := agentstate.NewStore(directory).Load()
	if err != nil || state.AgentID != "upgrade-smoke" || state.ServerURL != endpoint || len(state.ServerPins) != 1 || state.ServerPins[0] != identity.Fingerprint {
		t.Fatalf("pairing did not persist expected trust: %v", err)
	}
	client, err := agentclient.NewPaired(directory, "candidate")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if claim, err := client.Poll(ctx); err != nil || claim != nil {
		t.Fatalf("pinned poll failed: %v", err)
	}
	if err := waitForPoll(ctx, polls, "candidate"); err != nil {
		t.Fatal(err)
	}
	config, err := agenttls.NewClientConfig(endpoint, []agenttls.Fingerprint{identity.Fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	// The server certificate itself is a different client identity, not the
	// pinned agent. The fixture must reject it before reporting a poll.
	config.Certificates = []tls.Certificate{identity.Certificate}
	transport := &http.Transport{TLSClientConfig: config}
	defer transport.CloseIdleConnections()
	wrong := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := wrong.Post(endpoint+agentproto.PollPath, "application/json", bytes.NewBufferString(`{"protocol":"agent/1","agent_version":"wrong"}`))
	if err == nil {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
		t.Fatal("control plane accepted an unpinned client identity")
	}
}
