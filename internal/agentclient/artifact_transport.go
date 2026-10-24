package agentclient

import (
	"context"
	"net"
	"net/http"
	"time"
)

// artifactTransport isolates fetch timeouts from control-plane requests.
func (client *Client) artifactTransport() stagingTransport {
	policy := client.ResourcePolicy()
	source := client.http.Transport
	if original, ok := source.(*http.Transport); ok {
		transport := original.Clone()
		dial := transport.DialContext
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			ctx, cancel := context.WithTimeout(ctx, time.Duration(policy.Artifact.DialTimeoutSeconds)*time.Second)
			defer cancel()
			return dial(ctx, network, address)
		}
		transport.TLSHandshakeTimeout = time.Duration(policy.Artifact.TLSTimeoutSeconds) * time.Second
		transport.ResponseHeaderTimeout = time.Duration(policy.Artifact.HeaderTimeoutSeconds) * time.Second
		source = transport
	}
	return stagingTransport{
		source: source, idleTimeout: time.Duration(policy.Transfer.IdleTimeoutSeconds) * time.Second,
	}
}
