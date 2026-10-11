package executor

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if status, handled := HostSupervisorMain(); handled {
		os.Exit(status)
	}
	os.Exit(m.Run())
}
