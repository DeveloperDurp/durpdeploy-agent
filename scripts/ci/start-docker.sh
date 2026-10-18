#!/usr/bin/env bash
set -euo pipefail
bash scripts/ci/start-mirror.sh
# Run beside the tests so their Unix socket and temporary bind mounts are local.
nohup dockerd --host=unix:///run/durpdeploy-docker.sock --group=ci \
  --registry-mirror=http://127.0.0.1:5000 \
  --storage-driver=vfs --exec-opt native.cgroupdriver=cgroupfs \
  > /workspace/.ci/docker.log 2>&1 &
for ((attempt = 0; attempt < 60; attempt++)); do
  if docker --host=unix:///run/durpdeploy-docker.sock info >/dev/null 2>&1; then
    exit 0
  fi
  sleep 1
done
cat /workspace/.ci/docker.log >&2
exit 1
