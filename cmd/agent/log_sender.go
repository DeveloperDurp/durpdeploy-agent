package main

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/DeveloperDurp/durpdeploy-agent/internal/agentclient"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type logSender struct {
	ctx    context.Context
	client *agentclient.Client
	claim  agentproto.PollResponse
	mu     sync.Mutex
	next   agentproto.LogSequence
	events []agentproto.LogEvent
}

func newLogSender(
	ctx context.Context,
	client *agentclient.Client,
	claim agentproto.PollResponse,
) *logSender {
	return &logSender{ctx: ctx, client: client, claim: claim, next: 1}
}

func (sender *logSender) Write(line string) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	line = strings.ToValidUTF8(line, "\uFFFD")
	for {
		length := min(len(line), agentproto.MaxLogLineBytes)
		if length < len(line) {
			for !utf8.RuneStart(line[length]) {
				length--
			}
		}
		event := agentproto.LogEvent{Sequence: sender.next, Line: line[:length]}
		// All supported protocol versions have the same encoded length. Include
		// the envelope and JSON escaping when checking the batch byte limit.
		batch := agentproto.LogBatchRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV3,
			},
			ClaimToken: sender.claim.ClaimToken,
			Events:     append(sender.events, event),
		}
		raw, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		if len(raw) > agentproto.MaxLogBatchBytes {
			if err := sender.flushLocked(sender.ctx); err != nil {
				return err
			}
			batch.Events = []agentproto.LogEvent{event}
			raw, err = json.Marshal(batch)
			if err != nil {
				return err
			}
			if len(raw) > agentproto.MaxLogBatchBytes {
				return agentproto.ErrLogBatchTooLarge
			}
		}
		sender.events = batch.Events
		sender.next++
		if len(sender.events) >= agentproto.MaxLogEvents {
			if err := sender.flushLocked(sender.ctx); err != nil {
				return err
			}
		}
		line = line[length:]
		if line == "" {
			return nil
		}
	}
}

func (sender *logSender) Flush(ctx context.Context) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	return sender.flushLocked(ctx)
}

func (sender *logSender) flushLocked(ctx context.Context) error {
	if len(sender.events) == 0 {
		return nil
	}
	// A failed upload must not hold a process's output copier or cleanup forever.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := sender.client.Logs(
		ctx,
		sender.claim.DeploymentID,
		agentproto.LogBatchRequest{
			ClaimToken: sender.claim.ClaimToken, Events: sender.events,
		},
	); err != nil {
		return err
	}
	sender.events = nil
	return nil
}
