//go:build !linux

package executor

import "os"

// HostSupervisorMain refuses internal worker invocations outside Linux.
func HostSupervisorMain() (int, bool) {
	return 255, len(os.Args) > 1 && os.Args[1] == hostSupervisorArgument
}
