package agentclient

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	"github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/state"
	"github.com/DeveloperDurp/durpdeploy-agent/transport"
)

// Client makes only outbound HTTP requests to the configured agent server.
type Client struct {
	serverURL    string
	stateDir     string
	agentID      agentproto.AgentID
	agentVersion agentproto.AgentVersion
	protocol     agentproto.ProtocolVersion
	interpreters []executor.Interpreter
	container    *executor.ContainerExecutor
	identity     agenttls.Identity
	http         *http.Client
	state        agentstate.State

	mu     sync.Mutex
	pins   []agenttls.Fingerprint
	now    func() time.Time
	sleep  func(context.Context, time.Duration) error
	jitter func(int64) (int64, error)
}

// SupportsInterpreter reports whether the interpreter was discovered inside
// the same execution boundary used by this client.
func (client *Client) SupportsInterpreter(
	interpreter executor.Interpreter,
) bool {
	for _, supported := range client.interpreters {
		if interpreter == supported {
			return true
		}
	}
	return false
}

// Close releases idle outbound connections. It never starts a listener.
func (client *Client) Close() {
	client.http.CloseIdleConnections()
	if client.container != nil {
		if err := client.container.Close(); err != nil {
			slog.Warn("remove private runtime client home", "err", err)
		}
	}
}

// StateDir returns the private directory holding this client's identity and pins.
func (client *Client) StateDir() string { return client.stateDir }
