#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
if ! grep -Fq -- '--read-only' "$repo_root/Makefile"; then
	echo 'agent container contract negative test: agent-run root is writable' >&2
	exit 1
fi

assert_rejected() {
	local target=$1 payload=$2 diagnostic=$3 fixture
	fixture=$(mktemp -d)
	mkdir -p "$fixture/bootstrap" "$fixture/executor"
	cp "$repo_root/Dockerfile" "$repo_root/Makefile" \
		"$repo_root/agent-entrypoint.sh" "$repo_root/compose.yml" \
		"$repo_root/compose.example.yml" "$fixture/"
	cp "$repo_root/bootstrap/listener.go" "$repo_root/bootstrap/commit.go" \
		"$fixture/bootstrap/"
	cp "$repo_root/executor/executor.go" "$repo_root/executor/sandbox_linux.go" \
		"$fixture/executor/"
	printf '\n%s\n' "$payload" >> "$fixture/$target"
	if output=$(AGENT_CONTAINER_CONTRACT_ROOT="$fixture" \
		bash "$repo_root/scripts/check-agent-container-contract.sh" 2>&1); then
		echo "agent container contract negative test: accepted $diagnostic" >&2
		rm -rf "$fixture"
		exit 1
	fi
	if ! grep -Fq "$diagnostic" <<<"$output"; then
		printf 'agent container contract negative test: missing diagnostic %s\n%s\n' \
			"$diagnostic" "$output" >&2
		rm -rf "$fixture"
		exit 1
	fi
	rm -rf "$fixture"
}

assert_rejected executor/executor.go '// chroot' 'executor source invokes chroot'
assert_rejected Dockerfile 'SYS_CHROOT' 'Dockerfile requires SYS_CHROOT'
assert_rejected Dockerfile 'SYS_ADMIN' 'Dockerfile requires SYS_ADMIN'

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
