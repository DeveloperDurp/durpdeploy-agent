#!/usr/bin/env bash
set -euo pipefail

unit=${AGENT_SYSTEMD_UNIT:-systemd/durpdeploy-agent.service}
required=(
	'User=durpdeploy-agent'
	'Group=durpdeploy-agent'
	'EnvironmentFile=/etc/durpdeploy-agent.env'
	'Environment=DURPDEPLOY_AGENT_EXECUTION_BOUNDARY=service'
	'ExecStart=/usr/local/bin/durpdeploy-agent'
	'StateDirectory=durpdeploy-agent'
	'StateDirectoryMode=0700'
	'NoNewPrivileges=true'
	'ProtectSystem=strict'
	'ProtectHome=true'
	'PrivateTmp=true'
	'PrivateMounts=true'
	'PrivateDevices=true'
	'ProtectControlGroups=true'
	'RestrictNamespaces=true'
	'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6'
	'MemoryMax=512M'
	'TasksMax=128'
	'CapabilityBoundingSet='
	'AmbientCapabilities='
	'CPUQuota=100%'
)
for value in "${required[@]}"; do
	grep -Fqx "$value" "$unit" || {
		 echo "agent systemd contract: missing $value" >&2
		exit 1
	}
done

python3 - "$unit" <<'PY'
import pathlib
import sys

section = ""
for raw_line in pathlib.Path(sys.argv[1]).read_text().splitlines():
    line = raw_line.strip()
    if line.startswith("[") and line.endswith("]"):
        section = line[1:-1].casefold()
        continue
    if section != "service" or not line or line.startswith(("#", ";")):
        continue
    key, separator, value = line.partition("=")
    if not separator:
        continue
    key = key.strip().casefold()
    value = value.strip().casefold()
    if key in {"ambientcapabilities", "capabilityboundingset"} and value:
        raise SystemExit("agent systemd contract: forbidden capability grant")
    if key == "privatenetwork" and value == "true":
        raise SystemExit("agent systemd contract: forbidden PrivateNetwork=true")
    if key == "delegate" and value == "true":
        raise SystemExit("agent systemd contract: forbidden Delegate=true")
    if key == "protectcontrolgroups" and value != "true":
        raise SystemExit(
            "agent systemd contract: forbidden ProtectControlGroups=false"
        )
    if key == "restrictnamespaces" and value != "true":
        raise SystemExit(
            "agent systemd contract: forbidden RestrictNamespaces=false"
        )
    if key == "bindreadonlypaths":
        if "/var/lib/durpdeploy" in value:
            raise SystemExit(
                "agent systemd contract: forbidden "
                "BindReadOnlyPaths=/var/lib/durpdeploy"
            )
        if "/data" in value:
            raise SystemExit(
                "agent systemd contract: forbidden BindReadOnlyPaths=/data"
            )
    if key in {"bindpaths", "bindreadonlypaths"}:
        for socket in ("docker.sock", "podman.sock"):
            if socket in value:
                raise SystemExit(f"agent systemd contract: forbidden {socket}")
PY

if command -v systemd-analyze >/dev/null 2>&1; then
	set +e
	output=$(systemd-analyze verify "$unit" 2>&1)
	status=$?
	set -e
	if [ "$status" -ne 0 ] && ! grep -Fq 'Command /usr/local/bin/durpdeploy-agent is not executable' <<<"$output"; then
		printf '%s\n' "$output" >&2
		exit "$status"
	fi
	if [ "$status" -ne 0 ]; then
		echo 'agent systemd contract: static PASS (agent binary is not installed)'
	else
		echo 'agent systemd contract: systemd-analyze PASS'
	fi
else
	echo 'agent systemd contract: static PASS (systemd-analyze unavailable)'
fi
