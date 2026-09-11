#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_COMPOSE_CONTRACT_ROOT:-.}
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

if docker compose version >/dev/null 2>&1; then
	compose=(docker compose)
	compose_format=json
elif podman compose version >/dev/null 2>&1; then
	compose=(podman compose)
	compose_format=yaml
else
	echo 'agent compose contract: Docker Compose or Podman Compose is required' >&2
	exit 1
fi

for file in compose.yml compose.example.yml; do
	name=$(basename "$file" .yml)
	mkdir -p "$workdir/$name/secrets"
	cp "$root/$file" "$workdir/$name/compose.yml"
	: > "$workdir/$name/compose.app.env"
	: > "$workdir/$name/compose.caddy.env"
	: > "$workdir/$name/compose.litestream.env"
	: > "$workdir/$name/compose.agent.env"
	printf '%s\n' placeholder > "$workdir/$name/secrets/durpdeploy_key"
	"${compose[@]}" -f "$workdir/$name/compose.yml" config >/dev/null
	if [ "$compose_format" = json ]; then
		"${compose[@]}" -f "$workdir/$name/compose.yml" --profile agent config \
			--format json > "$workdir/$name/agent.json"
	else
		COMPOSE_PROFILES=agent "${compose[@]}" -f "$workdir/$name/compose.yml" config \
			> "$workdir/$name/agent.yml"
	fi
done

python3 - "$workdir" <<'PY'
import json
import pathlib
import sys

import yaml

root = pathlib.Path(sys.argv[1])
for path in list(root.glob("*/agent.json")) + list(root.glob("*/agent.yml")):
    document = json.loads(path.read_text()) if path.suffix == ".json" else yaml.safe_load(path.read_text())
    services = document["services"]
    agent = services["agent"]
    name = path.parent.name + ".yml"

    def fail(message):
        raise SystemExit(f"agent compose contract: {name} {message}")

    if agent["image"] != "ghcr.io/developerdurp/durpdeploy-agent:latest":
        fail("uses an unexpected image")
    if agent["cap_drop"] != ["ALL"]:
        fail("does not drop all capabilities")
    if agent["cap_add"] != ["SETUID", "SETGID", "SETPCAP"]:
        fail("adds capabilities outside the identity switch set")
    if agent.get("read_only") is not True:
        fail("permits a writable image root")
    security_opt = [option.replace("apparmor=", "apparmor:", 1) for option in agent["security_opt"]]
    if security_opt != ["no-new-privileges:true"]:
        fail("changes the approved security options")
    if str(agent.get("privileged", False)).lower() == "true":
        fail("enables privileged mode")
    if str(agent.get("pid", "")).lower() == "host":
        fail("shares the host PID namespace")
    if str(agent.get("network_mode", "")).lower() == "host":
        fail("shares the host network")
    volumes = []
    for volume in agent["volumes"]:
        if isinstance(volume, str):
            source, target, *options = volume.split(":")
            volume = {
                "source": source,
                "target": target,
                "type": "bind" if source.startswith("/") else "volume",
                "read_only": "ro" in options,
            }
        volumes.append(volume)
    agent_text = json.dumps(agent)
    if "/data" in agent_text:
        fail("mounts server data")
    if "docker.sock" in agent_text:
        fail("mounts a container socket")
    if len(volumes) != 1:
        fail("must have exactly one private state volume")
    state = volumes[0]
    if state["target"] != "/var/lib/durpdeploy-agent":
        fail("state volume uses the wrong target")
    if state["type"] != "volume":
        fail("state path is not a named volume")
    if not state["source"].endswith("durpdeploy-agent-state"):
        fail("state volume uses the wrong source")
    for forbidden in ("/data", "/sys/fs/cgroup", "durpdeploy_key", "docker.sock", "SYS_ADMIN", "SYS_CHROOT", "unconfined"):
        if forbidden in agent_text:
            fail(f"contains forbidden {forbidden}")
print("agent compose contract: PASS")
PY
