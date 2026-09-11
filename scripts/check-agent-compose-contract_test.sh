#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
checker="$repo_root/scripts/check-agent-compose-contract.sh"

assert_rejected() {
	local value=$1 diagnostic=$2 fixture output
	fixture=$(mktemp -d)
	cp "$repo_root/compose.yml" "$repo_root/compose.example.yml" "$fixture/"
	python3 - "$fixture/compose.yml" "$value" <<'PY'
import pathlib
import sys

import yaml

path = pathlib.Path(sys.argv[1])
document = yaml.safe_load(path.read_text())
key, value = sys.argv[2].split("=", 1)
document["services"]["agent"][key] = yaml.safe_load(value)
path.write_text(yaml.safe_dump(document, sort_keys=False))
PY
	if output=$(AGENT_COMPOSE_CONTRACT_ROOT="$fixture" bash "$checker" 2>&1); then
		echo "agent compose contract negative test: accepted $value" >&2
		rm -rf "$fixture"
		exit 1
	fi
	if ! grep -Fq "$diagnostic" <<<"$output"; then
		printf 'agent compose contract negative test: missing diagnostic %s\n%s\n' \
			"$diagnostic" "$output" >&2
		rm -rf "$fixture"
		exit 1
	fi
	rm -rf "$fixture"
}

assert_rejected 'privileged=true' 'compose.yml enables privileged mode'
assert_rejected 'privileged="true"' 'compose.yml enables privileged mode'
assert_rejected 'pid=host' 'compose.yml shares the host PID namespace'
assert_rejected 'pid="host"' 'compose.yml shares the host PID namespace'
assert_rejected 'network_mode=host' 'compose.yml shares the host network'
assert_rejected 'network_mode="host"' 'compose.yml shares the host network'
assert_rejected 'read_only=false' 'compose.yml permits a writable image root'
assert_rejected 'read_only="false"' 'compose.yml permits a writable image root'
for capability in SYS_ADMIN sys_admin CAP_SYS_ADMIN cap_sys_admin; do
	assert_rejected "cap_add=[\"$capability\"]" \
		'compose.yml adds capabilities outside the identity switch set'
done
assert_rejected 'volumes=["/data:/data"]' 'compose.yml mounts server data'
assert_rejected 'volumes=["/var/run/docker.sock:/var/run/docker.sock"]' \
	'compose.yml mounts a container socket'
assert_rejected 'volumes=["/run/podman/podman.sock:/run/podman/podman.sock"]' \
	'compose.yml mounts a container socket'

comment_fixture=$(mktemp -d)
trap 'rm -rf "$comment_fixture"' EXIT
cp "$repo_root/compose.yml" "$repo_root/compose.example.yml" "$comment_fixture/"
printf '%s\n' '# cap_add: [sys_admin] /run/podman/podman.sock' \
	>> "$comment_fixture/compose.yml"
AGENT_COMPOSE_CONTRACT_ROOT="$comment_fixture" bash "$checker" >/dev/null

printf '%s\n' 'agent compose contract negative test: PASS'
