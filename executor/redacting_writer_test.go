package executor

import (
	"strings"
	"testing"
)

func TestRedactingWriter_redacts_multiline_secrets_in_whole_and_fragmented_writes(
	t *testing.T,
) {
	for _, chunks := range [][]string{
		{"BEGIN\nprivate-material\nEND\n"},
		{"BEGIN\nprivate-", "material\nEND\n"},
	} {
		// Given
		var logs []string
		writer := newRedactingWriter(
			NewScrubber([]string{"BEGIN\nprivate-material\nEND"}),
			func(line string) error {
				logs = append(logs, line)
				return nil
			},
		)
		// When
		for _, chunk := range chunks {
			if _, err := writer.Write([]byte(chunk)); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.flush(); err != nil {
			t.Fatal(err)
		}
		// Then
		if got := strings.Join(
			logs,
			"\n",
		); got != "[REDACTED]\n[REDACTED]\n[REDACTED]" {
			t.Fatalf("multiline secret leaked: %q", got)
		}
	}
}

func TestRedactingWriter_redacts_secrets_across_writes(t *testing.T) {
	// Given
	var logs []string
	writer := newRedactingWriter(
		NewScrubber([]string{"private-value"}),
		func(line string) error {
			logs = append(logs, line)
			return nil
		},
	)
	// When
	for _, chunk := range []string{"first private-", "value\nsecond\ntail"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.flush(); err != nil {
		t.Fatal(err)
	}
	// Then
	if got := strings.Join(
		logs,
		"\n",
	); got != "first [REDACTED]\nsecond\ntail" {
		t.Fatalf("logs: %q", got)
	}
}
