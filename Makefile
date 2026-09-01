.PHONY: build test e2e container container-contract compose-contract systemd-contract documentation-contract check

BINARY_NAME := durpdeploy-agent
IMAGE ?= durpdeploy-agent:local

build:
	go build -o $(BINARY_NAME) ./cmd/agent

test:
	go test -count=1 ./...

e2e:
	go test -count=1 -tags agenttest ./cmd/agent -run '^TestAgentSubprocess_'

container:
	docker build -t $(IMAGE) .

container-contract:
	AGENT_CONTAINER_IMAGE=$(IMAGE) bash ./scripts/check-agent-container-contract.sh

compose-contract:
	bash ./scripts/check-agent-compose-contract.sh

systemd-contract:
	bash ./scripts/check-agent-systemd-contract.sh

documentation-contract:
	bash ./scripts/check-agent-documentation-contract.sh

check: test e2e compose-contract systemd-contract documentation-contract
