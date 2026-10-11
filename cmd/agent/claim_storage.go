package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func persistTerminalResult(
	client *agentclient.Client,
	claim agentproto.PollResponse,
	request agentproto.Request,
) error {
	sealed, err := client.SealTerminalReport(claim.DeploymentID, request)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	return writeClaimMarker(client, claimMarker{
		DeploymentID: int64(
			claim.DeploymentID,
		),
		TokenHash:      fmt.Sprintf("%x", digest),
		TerminalReport: sealed,
	})
}

func persistClaim(
	client *agentclient.Client,
	claim agentproto.PollResponse,
	phase claimPhase,
) error {
	// Legacy markers retain only a hash. Started container claims also retain an
	// encrypted terminal report before any workload can begin.
	digest := sha256.Sum256([]byte(claim.ClaimToken))
	marker := claimMarker{
		DeploymentID: int64(claim.DeploymentID),
		TokenHash:    fmt.Sprintf("%x", digest),
	}
	switch phase {
	case claimLegacy:
	case claimCleanupPending, claimCleanupAcknowledged:
		sealed, err := client.SealCleanupReport(claim)
		if err != nil {
			return err
		}
		marker.CleanupResult = sealed
		marker.Runtime = client.ContainerExecutor().Runtime()
		marker.SocketURL = client.ContainerExecutor().SocketURL()
		marker.CleanupAcknowledged = phase == claimCleanupAcknowledged
	}
	return writeClaimMarker(client, marker)
}

func writeClaimMarker(client *agentclient.Client, marker claimMarker) error {
	contents, err := json.Marshal(marker)
	if err != nil {
		return err
	}
	path := filepath.Join(client.StateDir(), claimFileName)
	return writePrivateFile(path, contents)
}

func resumeCleanupResult(
	ctx context.Context,
	client *agentclient.Client,
) error {
	path := filepath.Join(client.StateDir(), claimFileName)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pending claim: %w", err)
	}
	var marker claimMarker
	if err := json.Unmarshal(raw, &marker); err != nil {
		return fmt.Errorf("decode pending claim: %w", err)
	}
	id := agentproto.DeploymentID(marker.DeploymentID)
	if len(marker.TerminalReport) > 0 {
		if len(marker.CleanupResult) > 0 {
			return fmt.Errorf(
				"pending claim contains conflicting recovery reports",
			)
		}
		request, err := client.DecodeTerminalReport(id, marker.TerminalReport)
		if err != nil {
			return err
		}
		if !marker.TerminalAcknowledged {
			if err := client.ReportTerminal(ctx, id, request); err != nil {
				return err
			}
			marker.TerminalAcknowledged = true
			if err := writeClaimMarker(client, marker); err != nil {
				return err
			}
		}
		return clearClaim(client)
	}
	if len(marker.CleanupResult) == 0 {
		return nil
	}
	result, err := client.DecodeCleanupReport(id, marker.CleanupResult)
	if err != nil {
		return err
	}
	if client.ContainerExecutor() == nil {
		return fmt.Errorf(
			"restore container configuration to reconcile pending claim: %w",
			runner.ErrContainerCleanup,
		)
	}
	if marker.Runtime != client.ContainerExecutor().Runtime() ||
		marker.SocketURL != client.ContainerExecutor().SocketURL() {
		return fmt.Errorf(
			"restore original runtime and socket to reconcile pending claim: %w",
			runner.ErrContainerCleanup,
		)
	}
	if err := client.ValidateExecutionReady(ctx); err != nil {
		return err
	}
	if !marker.CleanupAcknowledged {
		if err := client.Result(ctx, id, result); err != nil {
			return err
		}
		marker.CleanupAcknowledged = true
		if err := writeClaimMarker(client, marker); err != nil {
			return err
		}
	}
	return clearClaim(client)
}

func clearClaim(client *agentclient.Client) error {
	if err := os.Remove(
		filepath.Join(client.StateDir(), claimFileName),
	); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncClaimDirectory(client.StateDir())
}

func writePrivateFile(path string, contents []byte) (err error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	defer func() {
		if removeErr := os.Remove(
			temporaryPath,
		); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) &&
			err == nil {
			err = removeErr
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		return errors.Join(err, file.Close())
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncClaimDirectory(filepath.Dir(path))
}

func syncClaimDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
