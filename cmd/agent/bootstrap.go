package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
)

func runBootstrap(ctx context.Context, config agentbootstrap.Config) error {
	listener, err := agentbootstrap.Start(config)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		_ = listener.Shutdown(shutdownCtx)
	}()
	encoded, err := json.Marshal(listener.Offer().Code)
	if err != nil {
		return fmt.Errorf("encode pairing code: %w", err)
	}
	var code string
	if err := json.Unmarshal(encoded, &code); err != nil {
		return fmt.Errorf("decode pairing code: %w", err)
	}
	if err := writePairingOffer(os.Stdout, code, listener.Offer().AgentPin.String()); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-listener.Paired():
		return nil
	case <-listener.Done():
		return agentstate.ErrRePairRequired
	}
}

func writePairingOffer(output io.Writer, code, fingerprint string) error {
	if _, err := fmt.Fprintf(output, "Pairing code: %s\n", code); err != nil {
		return fmt.Errorf("write pairing code: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Agent fingerprint: %s\n", fingerprint); err != nil {
		return fmt.Errorf("write agent fingerprint: %w", err)
	}
	return nil
}
