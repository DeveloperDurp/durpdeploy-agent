package agentclient

import (
	"fmt"
	"net/http"

	"github.com/DeveloperDurp/durpdeploy-agent/artifact"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
)

func (client *Client) setPollCapabilities(path string, request *http.Request) error {
	if path != agentproto.PollPath {
		return nil
	}
	advertisement, err := client.ResourcePolicy().Advertisement()
	if err != nil {
		return err
	}
	request.Header.Set("X-DurpDeploy-Staging", "staging/1")
	request.Header.Set(artifact.CapabilityHeader, artifact.Capability)
	request.Header.Set(resourcepolicy.CapabilityHeader, advertisement)
	return nil
}

func (client *Client) checkPollPolicy(path string, response *http.Response) error {
	if path != agentproto.PollPath || response.StatusCode != http.StatusOK && response.StatusCode != http.StatusNoContent {
		return nil
	}
	values := response.Header.Values(resourcepolicy.CapabilityHeader)
	if len(values) > 1 || len(values) == 1 && values[0] == "" {
		return fmt.Errorf("%w: invalid policy acknowledgement", resourcepolicy.ErrIncompatible)
	}
	advertisement := ""
	if len(values) == 1 {
		advertisement = values[0]
	}
	return client.ResourcePolicy().CheckCompatibility(advertisement)
}
