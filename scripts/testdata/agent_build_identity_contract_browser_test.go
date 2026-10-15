//go:build e2e && agentbrowser

package api_test

import (
	"fmt"
	"os"
	"testing"
)

func TestAgentBuildIdentityBrowserContract(t *testing.T) {
	origin := os.Getenv("DURPDEPLOY_AGENT_FLEET_URL")
	id := os.Getenv("DURPDEPLOY_AGENT_FLEET_ID")
	version := os.Getenv("DURPDEPLOY_AGENT_FLEET_VERSION")
	if origin == "" || id == "" || version == "" {
		t.Fatal("fleet URL, agent ID, and expected version are required")
	}
	browser := startPackageBrowser(t)
	var cookie struct {
		Success bool `json:"success"`
	}
	browser.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": "fleet-admin", "url": origin, "httpOnly": true,
	}, &cookie)
	if !cookie.Success {
		t.Fatal("fleet browser session was not established")
	}
	for _, width := range []int{375, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		for _, path := range []string{"/admin/agents", "/admin/agents/" + id} {
			fleetBrowserNavigate(t, browser, origin+path)
			browser.wait(t, fmt.Sprintf("document.body.innerText.includes(%q)", version))
			browser.wait(t, "document.documentElement.scrollWidth <= innerWidth")
			name := "list"
			if path != "/admin/agents" {
				name = "detail"
			}
			browser.screenshot(t, fmt.Sprintf("agent-version-%d-%s", width, name))
			t.Logf("rendered %s at %dpx: %s", path, width, version)
		}
	}
}
