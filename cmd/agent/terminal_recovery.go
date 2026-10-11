package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func reportCompletedClaim(
	ctx context.Context,
	client *agentclient.Client,
	marker *claimMarker,
) error {
	id := agentproto.DeploymentID(marker.DeploymentID)
	request, err := client.DecodeTerminalReport(id, marker.TerminalReport)
	if err != nil {
		return err
	}
	err = client.ReportTerminal(ctx, id, request)
	result, completed := request.(agentproto.ResultRequest)
	var statusErr *agentclient.StatusError
	if !completed ||
		!errors.As(err, &statusErr) || statusErr.Status != http.StatusConflict {
		return err
	}
	sealed, confirmErr := client.ConfirmTerminalCancellation(ctx, id, result)
	if confirmErr != nil || len(sealed) == 0 {
		return errors.Join(err, confirmErr)
	}
	// Persist the new endpoint before delivery. A lost ACK must replay cancellation.
	marker.TerminalReport = sealed
	if err := writeClaimMarker(client, *marker); err != nil {
		return err
	}
	slog.Info("deployment cancellation confirmed during result recovery",
		"deployment_id", id)
	return reportCompletedClaim(ctx, client, marker)
}
