package agentserver_test

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
)

// This fixture is copied into the pinned server checkout by the agent workflow.
// The agent under test is always this PR's executable, not the server's module pin.
func TestAgentBuildIdentityFleetE2E(t *testing.T) {
	for _, initialOverride := range []string{"", "pairing-override"} {
		t.Run("pairing/"+initialOverride, func(t *testing.T) {
			testAgentBuildIdentityFleet(t, initialOverride)
		})
	}
}

func testAgentBuildIdentityFleet(t *testing.T, initialOverride string) {
	t.Helper()
	f := newAgentFixture(t)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.repo.SetSecretBox(box)
	endpoint, err := agentproto.ParsePullEndpoint(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := agentserver.NewPairingService(agentserver.PairingConfig{
		Repository: f.repo, Identity: f.serverIdentity,
		PullEndpoint: endpoint, Secrets: box, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	app := fleetAdminServer(t, f, manager)
	polls := make(chan string, 32)
	original := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == agentproto.PollPath {
			body, readErr := io.ReadAll(r.Body)
			if readErr != nil {
				t.Error(readErr)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			var request agentproto.PollRequest
			if err := json.Unmarshal(body, &request); err != nil {
				t.Error(err)
				return
			}
			select {
			case polls <- string(request.AgentVersion):
			default:
			}
		}
		original.ServeHTTP(w, r)
	})
	binary := os.Getenv("DURPDEPLOY_AGENT_E2E_BINARY")
	upgrade := os.Getenv("DURPDEPLOY_AGENT_E2E_UPGRADE_BINARY")
	if binary == "" || upgrade == "" {
		t.Fatal("set tagged and development agent executable paths")
	}
	identity := func(binary string) string {
		t.Helper()
		command := exec.Command(binary, "version")
		command.Env = []string{}
		output, err := command.Output()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(strings.TrimSpace(string(output)), "durpdeploy-agent ")
	}
	tagged, development := identity(binary), identity(upgrade)
	if !strings.HasPrefix(tagged, "v1.2.3+build.4 (build ") ||
		!strings.HasPrefix(development, "v0.0.0-dev.") {
		t.Fatalf("unexpected build identities: %q / %q", tagged, development)
	}
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := reservation.Addr().String()
	if err := reservation.Close(); err != nil {
		t.Fatal(err)
	}
	stateDir := t.TempDir()
	stop, lines := startVersionContractAgent(t, binary, stateDir, address, initialOverride)
	var code, pin string
	deadline := time.After(10 * time.Second)
	for code == "" || pin == "" {
		select {
		case line := <-lines:
			if strings.HasPrefix(line, "Pairing code: ") {
				code = strings.TrimPrefix(line, "Pairing code: ")
			}
			if strings.HasPrefix(line, "Agent fingerprint: ") {
				pin = strings.TrimPrefix(line, "Agent fingerprint: ")
			}
		case <-deadline:
			t.Fatal("real agent did not offer pairing")
		}
	}
	request, err := json.Marshal(map[string]string{
		"address": "https://" + address, "code": code, "fingerprint": pin,
	})
	if err != nil {
		t.Fatal(err)
	}
	var paired struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(fleetRequest(t, app, "POST", "/api/v1/admin/agents/pair",
		"admin", string(request), http.StatusCreated), &paired); err != nil || paired.AgentID == "" {
		t.Fatalf("pairing response: %v", err)
	}
	want := tagged
	if initialOverride != "" {
		want = initialOverride
	}
	saved, err := agentstate.NewStore(stateDir).Load()
	if err != nil || saved.AgentVersion != want {
		t.Fatalf("pairing did not persist reported build: %q, %v", saved.AgentVersion, err)
	}
	phase := 0
	assertReport := func(want string) {
		t.Helper()
		// Browser setup can queue more reports from the previous process.
		pollDeadline := time.After(10 * time.Second)
	waitForPoll:
		for {
			select {
			case got := <-polls:
				if got == want {
					break waitForPoll
				}
			case <-pollDeadline:
				t.Fatalf("agent did not poll with build identity %q", want)
			}
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			agent, err := f.repo.Queries.GetAgent(t.Context(), paired.AgentID)
			if err == nil && agent.AgentVersion.Valid && agent.AgentVersion.String == want {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("fleet did not receive version %q: %+v, %v", want, agent, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		type report struct {
			ID      string         `json:"id"`
			Version sql.NullString `json:"agent_version"`
		}
		var list []report
		if err := json.Unmarshal(fleetRequest(t, app, "GET", "/api/v1/admin/agents",
			"admin", "", 200), &list); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, agent := range list {
			if agent.ID == paired.AgentID {
				found = agent.Version.Valid && agent.Version.String == want
			}
		}
		var detail struct {
			Agent report `json:"agent"`
		}
		if err := json.Unmarshal(fleetRequest(t, app, "GET",
			"/api/v1/admin/agents/"+paired.AgentID, "admin", "", 200), &detail); err != nil {
			t.Fatal(err)
		}
		if !found || !detail.Agent.Version.Valid || detail.Agent.Version.String != want {
			t.Fatalf("fleet list/detail did not report %q: %+v / %+v", want, list, detail)
		}
		for _, path := range []string{"/admin/agents", "/admin/agents/" + paired.AgentID} {
			req, err := http.NewRequestWithContext(t.Context(), "GET", app.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(&http.Cookie{Name: "session", Value: "fleet-admin"})
			response, err := app.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != 200 ||
				!strings.Contains(string(body), html.EscapeString(want)) {
				t.Fatalf("fleet UI %s status=%d did not render %q: %v", path, response.StatusCode, want, err)
			}
			t.Logf("GET %s status=200 version=%s", path, want)
		}
		phase++
		browser := exec.Command("bash", "scripts/go_test.sh", "-tags=e2e,agentbrowser",
			"-count=1", "-timeout=2m", "-v", "-run", "^TestAgentBuildIdentityBrowserContract$",
			"./internal/handler/api")
		browser.Dir = "../.."
		evidence := os.Getenv("DURPDEPLOY_PACKAGE_UI_EVIDENCE")
		if evidence == "" {
			evidence = t.TempDir()
		}
		browser.Env = append(os.Environ(),
			"DURPDEPLOY_AGENT_FLEET_URL="+app.URL,
			"DURPDEPLOY_AGENT_FLEET_ID="+paired.AgentID,
			"DURPDEPLOY_AGENT_FLEET_VERSION="+want,
			"DURPDEPLOY_PACKAGE_UI_EVIDENCE="+filepath.Join(evidence,
				fmt.Sprintf("%s-%d", strings.ReplaceAll(t.Name(), "/", "-"), phase)),
		)
		if output, err := browser.CombinedOutput(); err != nil {
			t.Fatalf("fleet browser: %v: %s", err, output)
		} else {
			t.Logf("fleet browser: %s", output)
		}
	}
	assertReport(want)
	stop()
	for _, override := range []string{"", "poll-override", ""} {
		// Reuse the original paired state across an upgrade and override changes.
		// Queue a prior report to exercise slow browser setup deterministically.
		polls <- want
		stop, _ = startVersionContractAgent(t, upgrade, stateDir, address, override)
		want = development
		if override != "" {
			want = override
		}
		assertReport(want)
		stop()
	}
}

func startVersionContractAgent(t *testing.T, binary, stateDir, address, override string) (func(), <-chan string) {
	t.Helper()
	stderr, err := os.Create(filepath.Join(t.TempDir(), "agent.log"))
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"DURPDEPLOY_AGENT_STATE_DIR=" + stateDir,
		"DURPDEPLOY_AGENT_LISTEN_ADDR=" + address,
		"DURPDEPLOY_AGENT_VERSION=" + override,
	}
	command.Stderr = stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 4)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			default:
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = command.Process.Signal(syscall.SIGTERM)
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("agent exit: %v", err)
				}
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				<-done
				t.Error("agent did not stop")
			}
			_ = stderr.Close()
			if t.Failed() {
				output, _ := os.ReadFile(stderr.Name())
				t.Logf("agent diagnostics: %s", output)
			}
		})
	}
	t.Cleanup(stop)
	return stop, lines
}
