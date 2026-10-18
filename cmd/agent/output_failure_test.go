package main

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
)

type diagnosticFailureWriter struct{ allow, calls int }

func (w *diagnosticFailureWriter) Write(data []byte) (int, error) {
	w.calls++
	if w.calls > w.allow {
		return 0, errors.New("closed diagnostic output")
	}
	return len(data), nil
}

func TestDiagnosticsReportOutputFailureAndStopWriting(t *testing.T) {
	t.Setenv("DURPDEPLOY_AGENT_VERSION", "override")
	for _, scenario := range []struct {
		name     string
		argument string
		allow    int
	}{{"help", "help", 0}, {"version", "version", 0}, {"override", "version", 1}} {
		t.Run(scenario.name, func(t *testing.T) {
			var logs bytes.Buffer
			original := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(original) })
			writer := &diagnosticFailureWriter{allow: scenario.allow}
			if !writeAgentDiagnostic([]string{scenario.argument}, writer) || writer.calls != scenario.allow+1 {
				t.Fatal("diagnostic handler continued writing after output failed")
			}
			if !strings.Contains(logs.String(), "closed diagnostic output") {
				t.Fatal("diagnostic output failure was not reported")
			}
		})
	}
}

func TestBootstrapReturnsWhenPairingCodeCannotBeDisplayed(t *testing.T) {
	output, err := os.CreateTemp(t.TempDir(), "closed-output")
	if err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = output
	t.Cleanup(func() { os.Stdout = original })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	err = runBootstrap(ctx, agentbootstrap.Config{StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0"})
	if err == nil || !strings.Contains(err.Error(), "write pairing code") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("bootstrap remained active without a visible pairing code: %v", err)
	}
}

func TestPairingOfferPropagatesEitherOutputFailure(t *testing.T) {
	for allowed, want := range []string{"write pairing code", "write agent fingerprint"} {
		writer := &diagnosticFailureWriter{allow: allowed}
		err := writePairingOffer(writer, "test-code", "test-fingerprint")
		if err == nil || !strings.Contains(err.Error(), want) || writer.calls != allowed+1 {
			t.Fatalf("pairing offer ignored output failure: %v, calls=%d", err, writer.calls)
		}
	}
	writer := &diagnosticFailureWriter{allow: 2}
	if err := writePairingOffer(writer, "test-code", "test-fingerprint"); err != nil || writer.calls != 2 {
		t.Fatalf("pairing offer could not display both values: %v", err)
	}
}
