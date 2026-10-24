package resourcepolicy

import (
	"errors"
	"reflect"
)

// Validate checks artifact budgets before parser reads and extraction effects.
func (p Artifact) Validate() error {
	if err := validateNumbers(reflect.ValueOf(p), "limits.artifact"); err != nil {
		return err
	}
	if p.StagingNodes < p.Files {
		return errors.New("artifact staging nodes must cover the file count")
	}
	return nil
}
