package agentclient

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestPairedClientPreservesStagingBeforeRecovery(t *testing.T) {
	identity := testIdentity(t)
	server := newTLSServer(
		t,
		identity,
		func(http.ResponseWriter, *http.Request) {},
	)
	client := testClient(t, server.URL, identity.Fingerprint.String())
	directory := filepath.Join(client.StateDir(), "staging")
	before, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "owned")
	if err := os.WriteFile(path, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewPaired(client.StateDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	after, err := os.Stat(directory)
	data, readErr := os.ReadFile(path)
	if err != nil || !os.SameFile(before, after) || readErr != nil ||
		string(data) != "retained" {
		t.Fatalf(
			"startup changed unvalidated staging: %v %q %v",
			err,
			data,
			readErr,
		)
	}
}

func TestResetStagingKeepsRootIdentityAndRejectsLinkTraversal(t *testing.T) {
	for _, link := range []bool{false, true} {
		t.Run(
			map[bool]string{false: "owned-root", true: "symlink"}[link],
			func(t *testing.T) {
				client := &Client{stateDir: t.TempDir()}
				directory := filepath.Join(client.StateDir(), "staging")
				if err := os.Mkdir(directory, 0700); err != nil {
					t.Fatal(err)
				}
				before, err := os.Stat(directory)
				if err != nil {
					t.Fatal(err)
				}
				external := t.TempDir()
				if link {
					if err := os.Remove(directory); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(external, directory); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.WriteFile(
					filepath.Join(directory, "owned"),
					[]byte("retained"),
					0600,
				); err != nil {
					t.Fatal(err)
				}
				if err := client.ResetStaging(); err != nil {
					t.Fatal(err)
				}
				after, err := os.Lstat(directory)
				entries, readErr := os.ReadDir(directory)
				if err != nil || !after.IsDir() || readErr != nil ||
					len(entries) != 0 {
					t.Fatalf("staging reset: %v %v %v", err, entries, readErr)
				}
				if !link && !os.SameFile(before, after) {
					t.Fatal("reset replaced the recorded staging root")
				}
				if link {
					data, err := os.ReadFile(filepath.Join(external, "owned"))
					if err != nil || string(data) != "retained" {
						t.Fatalf("reset followed the link: %q %v", data, err)
					}
				}
			},
		)
	}
}
