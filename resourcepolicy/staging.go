package resourcepolicy

import (
	"errors"
	"reflect"
)

// Validate checks staging budgets before archive I/O or filesystem effects.
func (p Staging) Validate() error {
	if err := validateNumbers(reflect.ValueOf(p), "limits.staging"); err != nil {
		return err
	}
	if p.ArchiveBytes < 1024 || p.PayloadBytes > p.ArchiveBytes-1024 {
		return errors.New("staging archive must allow payload and TAR framing")
	}
	return nil
}
