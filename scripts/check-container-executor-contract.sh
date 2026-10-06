#!/usr/bin/env bash
set -euo pipefail

: "${AGENT_TEST_RUNTIME:?Select docker or podman}"
: "${AGENT_TEST_SOCKET:?Set an absolute unix socket URL}"
: "${AGENT_TEST_IMAGE:?Set the locally built agent image}"
case "$AGENT_TEST_RUNTIME" in
  docker|podman) ;;
  *) echo 'runtime must be docker or podman' >&2; exit 1 ;;
esac

go test -race -count=1 -tags containertest ./executor -run '^TestContainerLive_'
go test -race -count=1 -tags containertest ./cmd/agent -run '^TestAgentContainerLive_'
