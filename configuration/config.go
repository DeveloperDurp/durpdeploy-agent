// Package configuration resolves startup inputs without changing process state.
// Runtime consumers must capture the result before starting the agent.
package configuration

import "github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"

// Config includes all application-owned agent startup inputs. Textual legacy
// settings keep their existing consumer validators and empty-value meanings.
type Config struct {
	StateDir           string                `yaml:"state_dir" env:"DURPDEPLOY_AGENT_STATE_DIR"`
	ListenAddr         string                `yaml:"listen_addr" env:"DURPDEPLOY_AGENT_LISTEN_ADDR"`
	Version            string                `yaml:"version" env:"DURPDEPLOY_AGENT_VERSION"`
	Hostname           string                `yaml:"hostname" env:"DURPDEPLOY_AGENT_HOSTNAME"`
	ExecutionBoundary  string                `yaml:"execution_boundary" env:"DURPDEPLOY_AGENT_EXECUTION_BOUNDARY"`
	ExtraScrubPatterns string                `yaml:"extra_scrub_patterns" env:"DURPDEPLOY_EXTRA_SCRUB_PATTERNS"`
	Container          Container             `yaml:"container"`
	Limits             resourcepolicy.Limits `yaml:"limits"`
}

type Container struct {
	Enabled string `yaml:"enabled" env:"DURPDEPLOY_AGENT_CONTAINER_ENABLED"`
	Runtime string `yaml:"runtime" env:"DURPDEPLOY_AGENT_CONTAINER_RUNTIME"`
	Socket  string `yaml:"socket" env:"DURPDEPLOY_AGENT_CONTAINER_SOCKET"`
}

func Defaults() Config {
	return Config{Limits: resourcepolicy.Defaults()}
}
