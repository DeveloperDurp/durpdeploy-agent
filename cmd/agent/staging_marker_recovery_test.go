//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestHostRecoveryPersistsProgressBeforeParentPurge(t *testing.T) {
	fixture := newAgentSubprocessFixture(t, "true")
	client, _ := claimLifecycleClient(t, fixture)
	defer client.Close()
	claim, err := client.Poll(t.Context())
	if err != nil || claim == nil {
		t.Fatalf("claim: %v %v", claim, err)
	}
	directory := filepath.Join(client.StateDir(), "staging")
	workspace := filepath.Join(directory, "workspace")
	if err := os.Mkdir(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(workspace, "data"),
		[]byte("owned"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	root, err := os.Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	child, err := os.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := child.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			t.Error(err)
		}
	})
	stage := &jobStaging{root: root, workspace: child}
	if err := stage.recordRoots(true); err != nil {
		t.Fatal(err)
	}
	if len(stage.recoveryRoots) != 2 ||
		stage.recoveryRoots[0].Path != workspace {
		t.Fatalf("lost ordered pre-launch identities: %+v", stage.recoveryRoots)
	}
	validParent := stage.recoveryRoots[1]
	// An invalid second identity stops recovery after the first purge.
	stage.recoveryRoots[1] = &stagingRootRecord{Path: directory}
	if err := persistHostStagingCleanup(
		client,
		*claim,
		stage.recoveryRoots,
	); err != nil {
		t.Fatal(err)
	}
	if err := resumeCleanupResult(
		t.Context(),
		client,
	); !errors.Is(
		err,
		executor.ErrContainerCleanup,
	) {
		t.Fatalf("accepted an unconfirmed parent: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(client.StateDir(), claimFileName))
	if err != nil {
		t.Fatal(err)
	}
	var marker claimMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatal(err)
	}
	if len(marker.HostStagingRoots) != 1 ||
		marker.HostStagingRoots[0].Path != directory {
		t.Fatalf(
			"did not persist completed workspace cleanup: %+v",
			marker.HostStagingRoots,
		)
	}
	select {
	case <-fixture.result:
		t.Fatal("reported completion before confirming all roots")
	default:
	}
	marker.HostStagingRoots = []*stagingRootRecord{validParent}
	if err := writeClaimMarker(client, marker); err != nil {
		t.Fatal(err)
	}
	if err := resumeCleanupResult(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if result := <-fixture.result; result.State != agentproto.ResultFailed {
		t.Fatalf("replayed result %s", result.State)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 0 {
		t.Fatalf("completed recovery retained staging: %v %v", entries, err)
	}
}
