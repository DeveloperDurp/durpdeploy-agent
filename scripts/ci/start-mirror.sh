#!/usr/bin/env bash
set -euo pipefail
# Docker mirrors cannot directly express Harbor's extra repository path prefix.
# Keep canonical Docker Hub digest lookups while routing them through Harbor.
go build -buildvcs=false -o /tmp/durpdeploy-registry-mirror ./scripts/ci/registry-mirror
nohup /tmp/durpdeploy-registry-mirror > /workspace/.ci/registry-mirror.log 2>&1 &
for ((attempt = 0; attempt < 30; attempt++)); do
  if python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:5000/v2/")' 2>/dev/null; then
    exit 0
  fi
  sleep 1
done
cat /workspace/.ci/registry-mirror.log >&2
exit 1
