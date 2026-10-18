package agentpayload

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func FuzzOpenEnvelope(f *testing.F) {
	// Stable test-only keys let saved corpus entries replay across test runs.
	private := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	other := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{2}, ed25519.SeedSize))
	template := &x509.Certificate{SerialNumber: big.NewInt(1)}
	certificate, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		f.Fatal(err)
	}
	identity := agenttls.Identity{Certificate: tls.Certificate{PrivateKey: private}}
	wrongIdentity := agenttls.Identity{Certificate: tls.Certificate{PrivateKey: other}}
	seed, err := Seal(certificate, 42, []byte("deployment payload"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte(`{"version":"agent-envelope/1","ephemeral_public":"invalid"}`))
	f.Add([]byte(`{"version":"unsupported"}`))
	f.Add([]byte("null"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > 64<<10 {
			t.Skip()
		}
		plaintext, err := Open(identity, 42, encoded)
		if err != nil {
			if plaintext != nil {
				t.Fatal("failed authentication exposed plaintext")
			}
			return
		}
		for _, attempt := range []struct {
			identity agenttls.Identity
			id       int64
		}{{wrongIdentity, 42}, {identity, 43}} {
			data, err := Open(attempt.identity, attempt.id, encoded)
			if err == nil || data != nil {
				t.Fatal("envelope accepted a different recipient or deployment")
			}
		}
		roundtrip, err := Seal(certificate, 42, plaintext)
		if err != nil {
			t.Fatal(err)
		}
		opened, err := Open(identity, 42, roundtrip)
		if err != nil || !bytes.Equal(opened, plaintext) {
			t.Fatalf("payload roundtrip changed: %v", err)
		}
		var tampered envelope
		if err := json.Unmarshal(encoded, &tampered); err != nil {
			t.Fatal(err)
		}
		ciphertext, err := base64.RawStdEncoding.DecodeString(tampered.Ciphertext)
		if err != nil || len(ciphertext) == 0 {
			t.Fatalf("accepted invalid ciphertext: %v", err)
		}
		ciphertext[0] ^= 1
		tampered.Ciphertext = base64.RawStdEncoding.EncodeToString(ciphertext)
		mutated, err := json.Marshal(tampered)
		if err != nil {
			t.Fatal(err)
		}
		if data, err := Open(identity, 42, mutated); err == nil || data != nil {
			t.Fatal("modified ciphertext passed authentication")
		}
	})
}
