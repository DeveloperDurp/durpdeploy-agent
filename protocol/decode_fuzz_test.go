package agentproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"
)

func FuzzDecodeRequest(f *testing.F) {
	for _, seed := range []string{
		`{"protocol":"agent/1","claim_token":"claim"}`,
		`{"protocol":"agent/2","claim_token":"claim","state":"succeeded"}`,
		`{"protocol":"agent/3","state":"cleanup_unconfirmed"}`,
		`{"protocol":"agent/1","events":[{"sequence":1,"line":"hello"}]}`,
		`{"protocol":"agent/1","protocol":"agent/2"}`,
		`{"protocol":"agent/1"} {}`,
		`{"protocol":"agent/99"}`,
		`{"protoCol":"agent/1"}`,
		"\xff", "", "null",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Size-limit rejection has dedicated unit tests. Keep mutations bounded.
		if len(data) > 16<<10 {
			t.Skip()
		}
		request, err := DecodeRequest[StartRequest](bytes.NewReader(data))
		if err == nil {
			encoded, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, err := DecodeRequest[StartRequest](bytes.NewReader(encoded))
			if err != nil || roundtrip != request {
				t.Fatalf("request roundtrip changed: %v", err)
			}
			checkDecodeBoundary(t, data, func(r io.Reader) error {
				_, err := DecodeRequest[StartRequest](r)
				return err
			})
		}
		result, err := DecodeRequest[ResultRequest](bytes.NewReader(data))
		if err == nil {
			if result.State == ResultCleanupUnconfirmed && result.Protocol != AgentV3 {
				t.Fatal("cleanup result accepted by an older protocol")
			}
			checkDecodeBoundary(t, data, func(r io.Reader) error {
				_, err := DecodeRequest[ResultRequest](r)
				return err
			})
		}
		batch, err := DecodeLogBatch(bytes.NewReader(data))
		if err == nil {
			encoded, err := json.Marshal(batch)
			if err != nil {
				t.Fatal(err)
			}
			roundtrip, err := DecodeLogBatch(bytes.NewReader(encoded))
			if err != nil || !reflect.DeepEqual(roundtrip, batch) {
				t.Fatalf("log batch roundtrip changed: %v", err)
			}
			checkDecodeBoundary(t, data, func(r io.Reader) error {
				_, err := DecodeLogBatch(r)
				return err
			})
		}
	})
}

func checkDecodeBoundary(t *testing.T, data []byte, decode func(io.Reader) error) {
	t.Helper()
	if err := decode(bytes.NewReader(append(append([]byte(nil), data...), []byte(" {}")...))); err == nil {
		t.Fatal("decoder accepted a second JSON value")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	// Duplicate an actual key, preserving its case. encoding/json also accepts
	// case-insensitive field matches, which are distinct JSON member names.
	for name, value := range fields {
		key, err := json.Marshal(name)
		if err != nil {
			t.Fatal(err)
		}
		duplicate := append([]byte{'{'}, key...)
		duplicate = append(duplicate, ':')
		duplicate = append(duplicate, value...)
		duplicate = append(duplicate, ',')
		duplicate = append(duplicate, bytes.TrimSpace(data)[1:]...)
		if err := decode(bytes.NewReader(duplicate)); !errors.Is(err, ErrInvalidJSON) {
			t.Fatalf("duplicate member was not rejected: %v", err)
		}
		break
	}
	fields["fuzz_unknown_field"] = json.RawMessage(`true`)
	unknown, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := decode(bytes.NewReader(unknown)); !errors.Is(err, ErrUnknownField) {
		t.Fatalf("unknown field was not rejected: %v", err)
	}
}
