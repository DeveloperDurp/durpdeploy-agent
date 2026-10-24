//go:build !linux

package executor

import "fmt"

func validateConfiguredExecutionBoundary(_ string) error {
	return fmt.Errorf("runner sandbox requires Linux")
}
