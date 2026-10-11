package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	runner "github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

// allow: SIZE_OK — claim states share heartbeat cancellation and recovery ownership.
func executeClaim(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) error {
	if err := persistClaim(client, claim, claimLegacy); err != nil {
		return err
	}
	clearMarker := true
	defer func() {
		if clearMarker {
			if clearErr := clearClaim(client); clearErr != nil {
				slog.Error("remove claim marker", "err", clearErr)
			}
		}
	}()
	plaintext, err := client.DecodePayload(claim)
	if err != nil {
		return err
	}
	payload, err := decodePayload(plaintext, int64(claim.DeploymentID))
	if err != nil {
		return err
	}
	if err := payload.validateExecution(client.SupportsStep); err != nil {
		return err
	}
	if err := client.ValidateExecutionReady(ctx); err != nil {
		return err
	}
	slog.Info(
		"deployment received",
		"deployment_id", claim.DeploymentID,
		"steps", len(payload.Release.Steps),
	)
	if err := client.Start(
		ctx,
		claim.DeploymentID,
		claim.ClaimToken,
	); err != nil {
		return err
	}
	slog.Info("deployment started", "deployment_id", claim.DeploymentID)
	if err := prepareContainerClaim(
		ctx,
		client,
		claim,
		payload.Release.Steps,
	); err != nil {
		clearMarker = false
		return err
	}
	executionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cancelled := false
	var cancelMu sync.Mutex
	logs := newLogSender(executionCtx, client, claim)
	heartbeatDone := make(chan struct{})
	go func() {
		defer close(heartbeatDone)
		ticker := time.NewTicker(agentproto.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				response, heartbeatErr := client.Heartbeat(
					executionCtx,
					claim.DeploymentID,
					claim.ClaimToken,
				)
				if heartbeatErr != nil {
					cancel()
					return
				}
				if response.CancelRequested {
					cancelMu.Lock()
					cancelled = true
					cancelMu.Unlock()
					cancel()
					return
				}
			}
		}
	}()

	environment, secrets, err := payload.environment()
	var stage *jobStaging
	if err == nil && payload.Release.Staging {
		stage, err = newJobStaging(executionCtx, client, claim)
	}
	if err == nil {
		executor := newExecutor()
		if client.ContainerExecutor() != nil {
			executor = runner.NewExecutorWithContainers(
				client.ContainerExecutor(),
			)
		}
		config := runner.ExecutionConfig{
			DeploymentID: int64(claim.DeploymentID),
			Steps:        payload.Release.Steps,
			Environment:  environment,
			Secrets:      secrets,
			CallbacksForStep: func(step runner.Step) runner.Callbacks {
				slog.Info(
					"step started",
					"deployment_id", claim.DeploymentID,
					"step", step.Name,
				)
				return runner.NewCallbacks(runner.CallbacksConfig{
					WriteLog: func(line string) error {
						slog.Info(
							"step output",
							"deployment_id", claim.DeploymentID,
							"step", step.Name,
							"output", line,
						)
						return logs.Write(line)
					},
					Cancelled: func() bool {
						cancelMu.Lock()
						defer cancelMu.Unlock()
						return cancelled
					},
				})
			},
			StepFinished: func(step runner.Step, stepErr error) {
				status := agentproto.ResultSucceeded
				if stepErr != nil {
					status = agentproto.ResultFailed
				}
				slog.Info(
					"step finished",
					"deployment_id", claim.DeploymentID,
					"step", step.Name,
					"status", status,
				)
			},
		}
		if stage != nil {
			err = stage.configure(
				executionCtx,
				client.ContainerExecutor(),
				&config,
			)
			if err == nil && stage.container == nil {
				err = executor.EnableHostSupervisor()
				if err == nil {
					err = persistHostStagingCleanup(client, claim)
				}
			}
		}
		if err == nil {
			err = executor.ExecuteSteps(executionCtx, config)
		}
		if err == nil && stage != nil {
			err = stage.publish(executionCtx, client, claim)
		}
	}
	if stage != nil && errors.Is(err, runner.ErrHostProcessCleanup) {
		// A failed supervisor requires service-wide process cleanup on restart.
		err = errors.Join(err, stage.input.Close(), stage.root.Close())
	} else if stage != nil {
		err = errors.Join(
			err,
			stage.close(context.WithoutCancel(executionCtx), client),
		)
	}
	cancel()
	<-heartbeatDone
	if flushErr := logs.Flush(ctx); flushErr != nil && err == nil {
		err = flushErr
	}
	cancelMu.Lock()
	wasCancelled := cancelled || errors.Is(err, runner.ErrCancelled) ||
		ctx.Err() != nil
	cancelMu.Unlock()
	if errors.Is(err, runner.ErrContainerCleanup) {
		clearMarker = false
		reportCtx, reportCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			agentproto.CancelAcknowledgementTimeout,
		)
		defer reportCancel()
		if stage != nil && stage.container == nil {
			return errors.Join(
				err,
				persistHostStagingCleanup(client, claim),
			)
		}
		if reportErr := client.Result(
			reportCtx,
			claim.DeploymentID,
			agentproto.ResultRequest{
				ClaimToken: claim.ClaimToken,
				State:      agentproto.ResultCleanupUnconfirmed,
				Error:      "Container cleanup could not be confirmed. Restore runtime access; the agent will reconcile its owned attempts before polling.",
			},
		); reportErr != nil {
			return errors.Join(err, reportErr)
		}
		return errors.Join(
			err,
			persistClaim(client, claim, claimCleanupAcknowledged),
		)
	}
	// Replace conservative cleanup state with the actual outcome before upload.
	// A failed upload must retain the claim authentication for restart recovery.
	clearMarker = false
	if wasCancelled {
		slog.Info(
			"deployment execution finished",
			"deployment_id", claim.DeploymentID,
			"status", "cancelled",
		)
		ackCtx, ackCancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			agentproto.CancelAcknowledgementTimeout,
		)
		defer ackCancel()
		if persistErr := persistTerminalResult(
			client,
			claim,
			agentproto.CancelledRequest{
				ClaimToken: claim.ClaimToken,
			},
		); persistErr != nil {
			return persistErr
		}
		return resumeCleanupResult(ackCtx, client)
	}
	result := agentproto.ResultSucceeded
	if err != nil {
		result = agentproto.ResultFailed
	}
	slog.Info(
		"deployment execution finished",
		"deployment_id", claim.DeploymentID,
		"status", result,
	)
	if persistErr := persistTerminalResult(
		client,
		claim,
		agentproto.ResultRequest{
			ClaimToken: claim.ClaimToken, State: result,
		},
	); persistErr != nil {
		return errors.Join(err, persistErr)
	}
	return resumeCleanupResult(ctx, client)
}
