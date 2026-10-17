package agentbootstrap

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/state"
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

func TestStart_portChangePreservesIdentityAndPairingState(t *testing.T) {
	stateDir := t.TempDir()
	identity, err := agenttls.LoadOrCreate(stateDir, "https://old-hostname")
	if err != nil {
		t.Fatal(err)
	}
	want, err := agentstate.New("https://server.example.test:10943",
		[]agenttls.Fingerprint{identity.Fingerprint}, "existing-agent")
	if err != nil {
		t.Fatal(err)
	}
	store := agentstate.NewStore(stateDir)
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"", "127.0.0.1:0"} {
		listener, err := Start(Config{StateDir: stateDir, ListenAddr: address})
		if err != nil {
			t.Fatal(err)
		}
		_, port, err := net.SplitHostPort(listener.listener.Addr().String())
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		shutdownErr := listener.Shutdown(ctx)
		cancel()
		if err != nil || shutdownErr != nil {
			t.Fatalf("address: %v; shutdown: %v", err, shutdownErr)
		}
		if address == "" && port != "23187" {
			t.Fatalf("default port = %s, want 23187", port)
		}
		if address != "" && port == "23187" {
			t.Fatal("explicit ephemeral listener used the default port")
		}
		if listener.identity.Fingerprint != identity.Fingerprint {
			t.Fatal("listener port change replaced the identity")
		}
		got, err := store.Load()
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("pairing state changed: %#v, %v", got, err)
		}
	}
}
