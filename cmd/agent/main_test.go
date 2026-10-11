package main

import (
	"os"
	"testing"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
)

func TestMain(m *testing.M) {
	if status, handled := executor.HostSupervisorMain(); handled {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
