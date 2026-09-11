.PHONY: build test e2e container container-contract compose-contract systemd-contract documentation-contract check agent-run

BINARY_NAME := durpdeploy-agent
IMAGE ?= durpdeploy-agent:local
AGENT_STATE_VOLUME ?= durpdeploy-agent-state
AGENT_PORT ?= 10943
AGENT_VERSION ?=

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
		--cap-add SETUID \
		--cap-add SETGID \
		--cap-add SETPCAP \
		--memory 512m \
		--cpus 1.0 \
		--pids-limit 128 \
		--env DURPDEPLOY_AGENT_LISTEN_ADDR=0.0.0.0:10943 \
		--env DURPDEPLOY_AGENT_STATE_DIR=/var/lib/durpdeploy-agent \
		$(if $(strip $(AGENT_VERSION)),--env DURPDEPLOY_AGENT_VERSION=$(AGENT_VERSION) )$(IMAGE)

container-contract:
	AGENT_CONTAINER_IMAGE=$(IMAGE) bash ./scripts/check-agent-container-contract.sh

compose-contract:
	bash ./scripts/check-agent-compose-contract.sh

systemd-contract:
	bash ./scripts/check-agent-systemd-contract.sh

documentation-contract:
	bash ./scripts/check-agent-documentation-contract.sh

check: test e2e compose-contract systemd-contract documentation-contract
