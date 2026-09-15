.PHONY: build dev test e2e container container-contract container-contract-test compose-contract compose-contract-test systemd-contract systemd-contract-test documentation-contract check agent-run

BINARY_NAME := durpdeploy-agent
IMAGE ?= durpdeploy-agent:local
AGENT_STATE_VOLUME ?= durpdeploy-agent-state
AGENT_PORT ?= 10943
AGENT_VERSION ?=

dev_port_origin := $(origin AGENT_PORT)
dev_port_value := $(AGENT_PORT)
# Keep the agent callback separate from the server listener by default while
# preserving explicit command-line and environment overrides.
dev: AGENT_PORT = $(if $(filter command line environment,$(dev_port_origin)),$(dev_port_value),10944)
dev: agent-run

build:
	go build -o $(BINARY_NAME) ./cmd/agent

test:
	go test -count=1 ./...

e2e:
	go test -count=1 -tags agenttest ./cmd/agent -run '^TestAgentSubprocess_'

container:
	podman build -t $(IMAGE) .

agent-run: container
	podman run --rm \
		--read-only \
		--publish $(AGENT_PORT):10943 \
		--volume $(AGENT_STATE_VOLUME):/var/lib/durpdeploy-agent \
		--tmpfs /tmp:size=64m,mode=1777 \
		--security-opt no-new-privileges=true \
		--cap-drop all \
		--memory 512m \
		--cpus 1.0 \
		--pids-limit 128 \
		--env DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943 \
		--env DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent \
		$(if $(strip $(AGENT_VERSION)),--env DURPDEPLOY_AGENT_VERSION=$(AGENT_VERSION) )$(IMAGE)

container-contract:
	AGENT_CONTAINER_IMAGE=$(IMAGE) bash ./scripts/check-agent-container-contract.sh

container-contract-test:
	bash ./scripts/check-agent-container-contract_test.sh

compose-contract:
	bash ./scripts/check-agent-compose-contract.sh

compose-contract-test:
	bash ./scripts/check-agent-compose-contract_test.sh

systemd-contract:
	bash ./scripts/check-agent-systemd-contract.sh

systemd-contract-test:
	bash ./scripts/check-agent-systemd-contract_test.sh

documentation-contract:
	bash ./scripts/check-agent-documentation-contract.sh

check: test e2e container-contract-test compose-contract compose-contract-test systemd-contract systemd-contract-test documentation-contract
