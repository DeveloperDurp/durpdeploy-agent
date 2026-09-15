#!/usr/bin/env bash
set -euo pipefail

require_text() {
	if ! grep -Fq -- "$2" "$1"; then
		echo "agent documentation contract: $3" >&2
		exit 1
	fi
}

for file in README.md docs/agents.md docs/agent-protocol.md; do
	test -f "$file" || {
		echo "agent documentation contract: missing $file" >&2
		exit 1
	}
done

require_text README.md 'not delete it during an upgrade.' \
	'upgrade state compatibility is not documented'
require_text docs/agents.md 'outbound-only' \
	'network boundary is not documented'
require_text docs/agents.md 'DURPDEPLOY_AGENT_STATE_DIR' \
	'state directory is not documented'
require_text docs/agent-protocol.md 'does not fall back to local execution' \
	'remote dispatch fallback is not explicit'
require_text docs/agent-protocol.md 'server-init' \
	'server-init pairing channel is missing'
require_text docs/agents.md 'does not use `chroot`' \
	'chroot-free execution boundary is not documented'
require_text docs/agents.md 'operator is responsible' \
	'operator script responsibility is not documented'
require_text docs/agents.md 'Read-only storage does not prevent' \
	'read-only boundary limitations are not documented'
require_text docs/agents.md 'host cgroup mount' \
	'host cgroup mount exclusion is not documented'
require_text README.md 'share one UID' \
	'shared service identity tradeoff is not documented'
require_text docs/agents.md 'Bash can' \
	'agent state exposure is not documented'

echo 'agent documentation contract: PASS'
