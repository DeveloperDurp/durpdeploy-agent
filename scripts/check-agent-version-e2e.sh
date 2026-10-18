#!/usr/bin/env bash
# Run an agent-owned contract with the pinned DurpDeploy server's real handlers.
# The supplied server checkout must have generated templ and Swagger UI assets.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
server=$(cd "${1:?usage: check-agent-version-e2e.sh SERVER_CHECKOUT}" && pwd)
directory=$(mktemp -d)
fixture="$server/internal/agentserver/agent_build_identity_contract_test.go"
browser_fixture="$server/internal/handler/api/agent_build_identity_contract_browser_test.go"
if [[ -e "$fixture" || -e "$browser_fixture" ]]; then
	echo 'Refusing to overwrite an existing server build-identity test' >&2
	rm -rf "$directory"
	exit 1
fi
trap 'rm -f "$fixture" "$browser_fixture"; rm -rf "$directory"' EXIT
cd "$root"
unset GITHUB_REF_TYPE GITHUB_REF_NAME
unset CI_COMMIT_TAG GITLAB_CI
commit=$(git rev-parse HEAD)
number=$(git rev-list --count HEAD)
BUILD_COMMIT=$commit BUILD_NUMBER=$number BUILD_MODIFIED=false BUILD_VERSION=v1.2.3+build.4 \
	bash scripts/build_agent.sh -o "$directory/tagged-agent"
BUILD_COMMIT=$commit BUILD_NUMBER=$number BUILD_MODIFIED=false \
	BUILD_VERSION="v0.0.0-dev.${number}+g${commit:0:12}" \
	bash scripts/build_agent.sh -o "$directory/development-agent"
cp scripts/testdata/agent_build_identity_contract_test.go "$fixture"
cp scripts/testdata/agent_build_identity_contract_browser_test.go "$browser_fixture"
cd "$server"
DURPDEPLOY_AGENT_E2E_BINARY="$directory/tagged-agent" \
DURPDEPLOY_AGENT_E2E_UPGRADE_BINARY="$directory/development-agent" \
	bash scripts/go_test.sh -race -count=1 -timeout=6m -v \
	-run '^TestAgentBuildIdentityFleetE2E$' ./internal/agentserver
