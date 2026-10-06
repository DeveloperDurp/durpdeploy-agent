package executor

import (
	"strings"
	"testing"
)

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
