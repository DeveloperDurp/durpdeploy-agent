//go:build linux

package executor

import (
	"fmt"
)

func validateConfiguredExecutionBoundary(boundary string) error {
	if boundary != "service" {
		return fmt.Errorf(
			"runner sandbox requires service execution boundary",
		)
	}
	return nil
}
