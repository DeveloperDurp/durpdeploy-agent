package resourcepolicy

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	CapabilityHeader = "X-DurpDeploy-Resource-Policy"
	Capability       = "resource-policy/1"
)

var ErrIncompatible = errors.New("incompatible agent resource policy")

// Advertisement identifies a validated snapshot without changing payload bodies.
// Version 1 hashes encoding/json's compact Limits representation in field order.
// Changes to that representation or enforcement semantics require a new version.
func (l Limits) Advertisement() (string, error) {
	if err := l.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(l)
	if err != nil {
		return "", errors.New("encode agent resource policy")
	}
	return fmt.Sprintf("%s;sha256=%x", Capability, sha256.Sum256(encoded)), nil
}

// CheckCompatibility requires the same policy from a policy-aware peer.
// An absent advertisement permits only the fixed legacy default policy.
// Callers must reject multiple header values and check before dispatch.
func (l Limits) CheckCompatibility(advertisement string) error {
	expected, err := l.Advertisement()
	if err != nil {
		return err
	}
	if advertisement == "" {
		if l != Defaults() {
			return fmt.Errorf("%w: custom limits require resource-policy support", ErrIncompatible)
		}
		return nil
	}
	if advertisement != expected {
		return fmt.Errorf("%w: advertised limits or policy version differ", ErrIncompatible)
	}
	return nil
}
