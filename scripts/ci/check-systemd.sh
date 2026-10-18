#!/usr/bin/env bash
set -euo pipefail
# Installation belongs only to the disposable systemd CI tools container.
[[ ${CI:-} == true && ${container:-} == docker && $(cat /proc/1/comm) == systemd ]]
[[ $(uname -m) == x86_64 && $(id -u) == 0 ]]
if systemctl is-active --quiet durpdeploy-agent.service; then
  echo 'refusing to replace an active agent service' >&2
  exit 1
fi
useradd --system --user-group --home-dir /var/lib/durpdeploy-agent \
  --shell /sbin/nologin durpdeploy-agent
install -d -o durpdeploy-agent -g durpdeploy-agent -m 0700 /var/lib/durpdeploy-agent
CGO_ENABLED=0 bash scripts/build_agent.sh -trimpath -o /usr/local/bin/durpdeploy-agent
install -m 0644 systemd/durpdeploy-agent.service /etc/systemd/system/
AGENT_SYSTEMD_SMOKE=true go test -count=1 -v -run '^TestLiveSystemd$' \
  ./scripts/ci/image-upgrade 2>&1 | tee .ci/systemd-smoke.log
