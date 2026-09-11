//go:build linux

package executor

import (
	"fmt"
	"os"
)

func validateExecutionBoundary() error {
	if os.Getenv("DURPDEPLOY_AGENT_EXECUTION_BOUNDARY") != "service" {
		return fmt.Errorf(
			"runner sandbox requires service execution boundary",
		)
	}
	return nil
}
