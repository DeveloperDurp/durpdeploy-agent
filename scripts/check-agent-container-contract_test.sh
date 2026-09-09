#!/usr/bin/env bash
set -euo pipefail

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
mkdir -p "$root/internal/agentbootstrap"
: > "$root/Dockerfile"
: > "$root/Makefile"
: > "$root/internal/agentbootstrap/listener.go"
: > "$root/internal/agentbootstrap/commit.go"

if output=$(AGENT_CONTAINER_CONTRACT_ROOT="$root" bash "$(dirname "$0")/check-agent-container-contract.sh" 2>&1); then
	echo 'agent container contract negative test: expected failure' >&2
	exit 1
fi
grep -Fq 'missing bootstrap/listener.go' <<<"$output"
printf '%s\n' 'agent container contract negative test: PASS'
