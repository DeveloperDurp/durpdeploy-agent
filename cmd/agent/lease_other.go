//go:build !linux

package main

import (
	"fmt"
	"os"
)

func acquireStateLease(string) (*os.File, error) {
	return nil, fmt.Errorf("agent state lease requires Linux")
}
