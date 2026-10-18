package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/staging"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestCorruptPackageFixtureRetainsValidZIP(t *testing.T) {
	fixture := newClaimPackageFixture(t, "corrupt-bytes")
	data := fixture.packageBytes()
	if int64(len(data)) != fixture.pin.Size || fmt.Sprintf("%x", sha256.Sum256(data)) == fixture.pin.SHA256 {
		t.Fatal("corrupt response must preserve size but change the pinned hash")
	}
	filename := filepath.Join(t.TempDir(), "changed.zip")
	if err := os.WriteFile(filename, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := artifact.ValidateZIP(t.Context(), filename); err != nil {
		t.Fatalf("checksum fixture can be rejected by ZIP validation instead: %v", err)
	}
}

func TestClaimPackageBecomesWritableStagingOutput(t *testing.T) {
	for _, scenario := range []string{"success", "moved-package", "invalid-zip", "changed-pin"} {
		t.Run(scenario, func(t *testing.T) { checkClaimPackage(t, scenario) })
	}
}

func checkClaimPackage(t *testing.T, scenario string) {
	t.Helper()
	fixture := newClaimPackageFixture(t, scenario)
	claim, err := fixture.client.Poll(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := executeClaim(t.Context(), fixture.client, *claim); err != nil {
		t.Fatal(err)
	}
	result := <-fixture.process.result
	want := agentproto.ResultSucceeded
	if scenario == "invalid-zip" || scenario == "changed-pin" {
		want = agentproto.ResultFailed
	}
	if result.State != want {
		t.Fatalf("result=%+v want=%s", result, want)
	}
	fixture.mu.Lock()
	output := append([]byte(nil), fixture.output...)
	fixture.mu.Unlock()
	if want == agentproto.ResultSucceeded {
		assertPackagePublication(t, output)
	} else if len(output) != 0 {
		t.Fatal("failed package was published")
	}
	assertPackageDirectoryEmpty(t, filepath.Join(fixture.process.stateDir, "staging"))
	if scenario == "moved-package" {
		assertPackageDirectoryEmpty(t, fixture.moved)
	}
}

func assertPackagePublication(t *testing.T, output []byte) {
	t.Helper()
	directory := t.TempDir()
	if err := staging.Unpack(t.Context(), bytes.NewReader(output), directory); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "output"))
	if err != nil || string(data) != "release" {
		t.Fatalf("published output=%q error=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "input")); !os.IsNotExist(err) {
		t.Fatalf("original package leaked into staging: %v", err)
	}
}

func assertPackageDirectoryEmpty(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("managed files retained: %s %v %v", directory, entries, err)
	}
}
