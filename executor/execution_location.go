package executor

import (
	"os"
	"strings"
)

// A socket-free container cannot discover its physical host. Operators can
// supply that name without granting the supervisor more host access.
func executionHostname(runtimeHost string) string {
	return configuredExecutionHostname(os.Getenv("DURPDEPLOY_AGENT_HOSTNAME"), runtimeHost)
}

func configuredExecutionHostname(override, runtimeHost string) string {
	if host := strings.TrimSpace(override); host != "" {
		return host
	}
	if strings.TrimSpace(runtimeHost) != "" {
		return runtimeHost
	}
	if os.Getenv("container") != "" {
		return "unknown"
	}
	for _, marker := range []string{"/.dockerenv", "/run/.containerenv"} {
		if _, err := os.Stat(marker); err == nil {
			return "unknown"
		}
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "unknown"
	}
	return host
}
