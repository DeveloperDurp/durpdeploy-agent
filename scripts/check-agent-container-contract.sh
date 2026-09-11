#!/usr/bin/env bash
set -euo pipefail

root=${AGENT_CONTAINER_CONTRACT_ROOT:-.}
image=${AGENT_CONTAINER_IMAGE:-durpdeploy-agent:contract}
state_volume="durpdeploy-agent-contract-state-$$"
trap 'podman volume rm -f "$state_volume" >/dev/null 2>&1 || true' EXIT

require_file() {
	if [ ! -f "$root/$1" ]; then
		echo "agent container contract: missing $1" >&2
		exit 1
	fi
}

require_text() {
	local file=$1 text=$2 description=$3
	if ! grep -Fq -- "$text" "$root/$file"; then
		echo "agent container contract: $description" >&2
		exit 1
	fi
}

forbid_text() {
	local file=$1 text=$2 description=$3
	if grep -Fq -- "$text" "$root/$file"; then
		echo "agent container contract: $description" >&2
		exit 1
	fi
}

require_file Dockerfile
require_file Makefile
require_file bootstrap/listener.go
require_file bootstrap/commit.go
require_text Dockerfile 'USER root' \
	'agent image must bootstrap the identity-switching capabilities as root'
require_text Dockerfile 'agent-entrypoint.sh' \
	'agent image must drop to its service identity in the entrypoint'
require_text Dockerfile 'durpdeploy-runner' \
	'agent image must create the distinct runner identity'
require_text Dockerfile 'DURPDEPLOY_AGENT_EXECUTION_BOUNDARY=service' \
	'agent image must activate the service execution boundary'
require_text Dockerfile 'util-linux' \
	'agent image must provide util-linux setpriv'
require_text Dockerfile 'VOLUME ["/var/lib/durpdeploy-agent", "/tmp"]' \
	'agent image must declare writable state and temporary volumes'
require_text Makefile 'build:' 'Make must build the agent binary'
require_text Makefile 'container:' 'Make must build the agent image'
require_text bootstrap/listener.go \
	'mux.HandleFunc(agentproto.ServerInitPath, listener.serverInit)' \
	'agent bootstrap must expose only the server-init pairing route'
forbid_text bootstrap/listener.go 'BootstrapPath' \
	'agent bootstrap must not restore the code-bearing GET route'
forbid_text bootstrap/listener.go '"/agent/v1/bootstrap"' \
	'agent bootstrap must not restore the code-bearing GET route'
for file in Dockerfile agent-entrypoint.sh Makefile compose.yml \
	compose.example.yml; do
	forbid_text "$file" 'SYS_ADMIN' "$file requires SYS_ADMIN"
	forbid_text "$file" 'SYS_CHROOT' "$file requires SYS_CHROOT"
	forbid_text "$file" 'apparmor=unconfined' \
		"$file disables the default AppArmor boundary"
done
forbid_text compose.yml '/sys/fs/cgroup' \
	'agent compose mounts the host cgroup filesystem'
forbid_text compose.example.yml '/sys/fs/cgroup' \
	'agent example mounts the host cgroup filesystem'
require_text bootstrap/listener.go 'ClientAuth:   tls.RequestClientCert,' \
	'agent bootstrap TLS must request the server client certificate'
require_text bootstrap/commit.go \
	'len(request.TLS.PeerCertificates) != 1' \
	'server-init must reject requests without exactly one client certificate'
require_text bootstrap/commit.go \
	'serverPin != pairRequest.ServerPin' \
	'server-init must bind the request server pin to the mTLS peer certificate'

podman build -f "$root/Dockerfile" -t "$image" "$root"

if [ "$(podman image inspect --format '{{.Config.User}}' "$image")" != root ]; then
	echo 'agent container contract: image user is not root for identity bootstrap' >&2
	exit 1
fi
if [ "$(podman image inspect --format '{{json .Config.ExposedPorts}}' "$image")" != null ]; then
	echo 'agent container contract: image must not expose a port' >&2
	exit 1
fi
if podman image inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$image" | \
	grep -Eq '^DURPDEPLOY_(SECRET_KEY|DB)='; then
	echo 'agent container contract: image includes server storage or secret configuration' >&2
	exit 1
fi

podman run --rm --read-only --security-opt no-new-privileges:true \
	--cap-drop ALL \
	--cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
	--memory 512m --cpus 1.0 --pids-limit 128 \
	--tmpfs /tmp:size=64m,mode=1777 \
	--volume "$state_volume:/var/lib/durpdeploy-agent" \
	"$image" sh -ceu '
	test -w /var/lib/durpdeploy-agent
	test -w /tmp
	test ! -w /
	command -v bash
	command -v setpriv
	! command -v curl
	! command -v docker
	! test -e /usr/local/bin/durpdeploy
	! test -e /data
	! test -S /var/run/docker.sock
	test "$(id -u)" = 10001
	test ! -w /usr
	test ! -w /
	cat > /tmp/runner-probe.sh <<"EOF"
test "$(id -u)" = 10002
test ! -r /var/lib/durpdeploy-agent
test ! -w /usr
test ! -w /
	for capability_set in CapInh CapPrm CapEff CapBnd CapAmb; do
		test "$(grep "^$capability_set:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 0000000000000000
	done
	test "$(grep "^NoNewPrivs:" /proc/self/status | tr -s "[:space:]" " " | cut -d " " -f 2)" = 1
	test "$(cat /sys/fs/cgroup/memory.max)" = 536870912
	test "$(cat /sys/fs/cgroup/pids.max)" = 128
	test "$(cat /sys/fs/cgroup/cpu.max)" = "100000 100000"
EOF
	chmod 0755 /tmp/runner-probe.sh
	setpriv --reuid=10002 --regid=10002 --clear-groups \
		--bounding-set=-all --inh-caps=-all --ambient-caps=-all \
		--no-new-privs -- /bin/bash /tmp/runner-probe.sh
'

help=$(podman run --rm --read-only \
	--security-opt no-new-privileges:true \
	--cap-drop ALL \
	--cap-add SETUID --cap-add SETGID --cap-add SETPCAP \
	--tmpfs /tmp:size=64m,mode=1777 \
	"$image" /usr/local/bin/durpdeploy-agent --help)
for required in DURPDEPLOY_AGENT_LISTEN_ADDR DURPDEPLOY_AGENT_STATE_DIR \
	DURPDEPLOY_AGENT_VERSION; do
	grep -Fq "$required" <<<"$help" || {
		echo "agent container contract: help omits $required" >&2
		exit 1
	}
done
if grep -Fq 'DURPDEPLOY_AGENT_SERVER_URL' <<<"$help"; then
	echo 'agent container contract: help exposes manual server configuration' >&2
	exit 1
fi

printf '%s\n' 'agent container contract: PASS'
