package agentbootstrap

import (
	"context"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestStart_reusesIdentityCreatedForPreviousHostname(t *testing.T) {
	stateDir := t.TempDir()
	created, err := agenttls.LoadOrCreate(stateDir, "https://old-hostname")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}

	listener, err := Start(Config{
		StateDir: stateDir, ListenAddr: "127.0.0.1:0",
	})
	if err != nil {
		t.Fatalf("start with persisted identity: %v", err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer func() {
		if err := listener.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shut down listener: %v", err)
		}
	}()

	if got := listener.identity.Fingerprint; got != created.Fingerprint {
		t.Fatalf("identity fingerprint = %s, want %s", got, created.Fingerprint)
	}
}
