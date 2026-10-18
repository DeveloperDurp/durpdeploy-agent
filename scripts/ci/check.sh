#!/usr/bin/env bash
set -euo pipefail
cd /workspace
git config --global --add safe.directory /workspace

# Preserve reports from failed tests without replacing their failure status.
if [[ ${1:?} == test || $1 == coverage ]]; then
  mkdir -p .ci/junit
  finish_reports() {
    local status=$?
    trap - EXIT
    python3 scripts/ci/gitlab_reports.py || status=1
    exit "$status"
  }
  trap finish_reports EXIT
fi

prepare_server() {
  git clone --no-checkout "${SERVER_REPOSITORY:?}" .ci/server
  git -C .ci/server checkout --detach "${SERVER_COMMIT:?}"
  (
    cd .ci/server
    go tool templ generate
    make swagger-ui-copy
  )
}

prepare_images() {
  # Keep the public contract's image names while fetching only through Harbor.
  # The executors inspect an existing local image before attempting a pull.
  local upstream proxy
  for upstream in docker.io/library/bash:5.2 docker.io/library/python:3.12-alpine \
    docker.io/library/alpine:3.20 mcr.microsoft.com/powershell:latest; do
    case "$upstream" in
      docker.io/*) proxy=harbor.durp.info/dockerhub/${upstream#docker.io/} ;;
      mcr.microsoft.com/*) proxy=harbor.durp.info/microsoft/${upstream#mcr.microsoft.com/} ;;
      *) printf 'Unsupported fixture image: %s\n' "$upstream" >&2; return 1 ;;
    esac
    "$AGENT_TEST_RUNTIME" pull "$proxy"
    "$AGENT_TEST_RUNTIME" tag "$proxy" "$upstream"
  done
}

prepare_browser_image() {
  docker pull harbor.durp.info/microsoft/playwright:v1.61.1-noble
  docker tag harbor.durp.info/microsoft/playwright:v1.61.1-noble \
    mcr.microsoft.com/playwright:v1.61.1-noble
}

case "${1:?}" in
  test)
    git ls-files -z -- '*.go' | xargs -0 -r gofmt -l | tee .ci/gofmt.log
    [[ ! -s .ci/gofmt.log ]]
    go vet ./...
    gotestsum --format standard-verbose --junitfile .ci/junit/race.xml -- \
      -race -shuffle=on -count=1 ./artifact ./protocol ./executor ./internal/agentclient ./cmd/agent
    make check GO_TEST='gotestsum --format standard-verbose --junitfile .ci/junit/make-$@.xml --'
    make build
    ;;
  docker|podman)
    export AGENT_TEST_RUNTIME=$1
    export AGENT_TEST_IMAGE=localhost/durpdeploy-agent:executor-contract
    export DURPDEPLOY_CONTAINER_RUNTIME=$1
    if [[ "$1" == docker ]]; then
      export AGENT_TEST_SOCKET=unix:///run/durpdeploy-docker.sock
      export DOCKER_HOST=$AGENT_TEST_SOCKET
    else
      systemctl --user start podman.socket
      export AGENT_TEST_SOCKET=unix://$XDG_RUNTIME_DIR/podman/podman.sock
      export DOCKER_HOST=$AGENT_TEST_SOCKET
      export TESTCONTAINERS_RYUK_DISABLED=true
      podman info
    fi
    prepare_images
    "$1" build --build-arg BUILD_VERSION --build-arg BUILD_NUMBER \
      --build-arg BUILD_COMMIT --build-arg BUILD_MODIFIED -t "$AGENT_TEST_IMAGE" .
    make container-executor-contract
    prepare_server
    (cd .ci/server && go test -tags=e2e,agentcontainer -race -count=1 -timeout=8m \
      -v -run '^TestAgentContainerAPIE2E$' ./internal/agentserver)
    ;;
  image-amd64)
    export DOCKER_HOST=unix:///run/durpdeploy-docker.sock
    bash scripts/ci/check-image-amd64.sh
    ;;
  systemd)
    bash scripts/ci/check-systemd.sh
    ;;
  version-e2e)
    export DOCKER_HOST=unix:///run/durpdeploy-docker.sock
    export DURPDEPLOY_CONTAINER_RUNTIME=docker
    export DURPDEPLOY_PACKAGE_UI_EVIDENCE=/workspace/.ci/agent-version-ui
    prepare_browser_image
    prepare_server
    bash scripts/check-agent-version-e2e.sh .ci/server
    [[ -n "$(find .ci/agent-version-ui -name '*.png' -print -quit)" ]]
    ;;
  coverage)
    python3 -m coverage run -m pytest --junitxml=.ci/junit/python.xml scripts/ci/*_test.py
    python3 -m coverage xml
    gotestsum --format standard-verbose --junitfile .ci/junit/go.xml -- \
      -count=1 -covermode=atomic -coverprofile=coverage.out ./...
    gocover-cobertura -strict < coverage.out > .ci/coverage-go-gitlab.xml
    go tool cover -func=coverage.out | tee .ci/coverage-go.log
    ;;
  release)
    mkdir -p dist
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 bash scripts/build_agent.sh -trimpath -o dist/durpdeploy-agent-linux-amd64
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 bash scripts/build_agent.sh -trimpath -o dist/durpdeploy-agent-linux-arm64
    [[ "$(dist/durpdeploy-agent-linux-amd64 version)" == \
      "durpdeploy-agent $BUILD_VERSION (build $BUILD_NUMBER, commit $BUILD_COMMIT)" ]]
    ;;
  *) printf 'Unknown CI check: %s\n' "$1" >&2; exit 1 ;;
esac
