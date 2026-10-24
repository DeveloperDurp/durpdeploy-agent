package agentclient

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPairedStartupSettingsSnapshotAndPollVersion(t *testing.T) {
	identity := testIdentity(t)
	polls := make(chan agentproto.PollRequest, 1)
	server := newTLSServer(t, identity, func(w http.ResponseWriter, r *http.Request) {
		var request agentproto.PollRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		polls <- request
		w.WriteHeader(http.StatusNoContent)
	})
	dir := t.TempDir()
	if _, err := agenttls.LoadOrCreate(dir, "https://127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	paired, err := agentstate.New(server.URL, []agenttls.Fingerprint{identity.Fingerprint}, "configured-agent")
	if err != nil {
		t.Fatal(err)
	}
	if err := agentstate.NewStore(dir).Save(paired); err != nil {
		t.Fatal(err)
	}
	settings := executor.StartupSettings{ExecutionBoundary: "service", Hostname: "captured-host", ExtraScrubPatterns: "CUSTOM_[0-9]+"}
	client, err := NewPairedWithSettings(dir, "captured-version", settings)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	settings.Hostname = "changed-caller"
	snapshot, ok := client.StartupSettings()
	if !ok || snapshot.Hostname != "captured-host" {
		t.Fatal("client did not capture startup settings")
	}
	snapshot.Hostname = "changed-returned-copy"
	if next, _ := client.StartupSettings(); next.Hostname != "captured-host" {
		t.Fatal("accessor returned mutable state")
	}
	if _, err := client.Poll(t.Context()); err != nil {
		t.Fatal(err)
	}
	if request := <-polls; request.AgentVersion != "captured-version" {
		t.Fatalf("poll version=%q", request.AgentVersion)
	}
	legacy, err := NewPaired(dir, "legacy-version")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(legacy.Close)
	if _, ok := legacy.StartupSettings(); ok {
		t.Fatal("legacy client acquired configured settings")
	}
}
