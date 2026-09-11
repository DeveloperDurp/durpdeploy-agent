//go:build !linux

package executor

import "fmt"

func validateExecutionBoundary() error {
	return fmt.Errorf("runner sandbox requires Linux")
}
