#!/bin/sh
set -eu
mkdir -p .ci
trap 'cat .ci/image-scan.log' EXIT
: > .ci/image-scan.log
{
trivy image --input .ci/agent-candidate --platform linux/amd64 --scanners vuln \
  --format cyclonedx --output .ci/agent-sbom.cdx.json
# Retain a full report, including findings with no available fix.
trivy image --input .ci/agent-candidate --platform linux/amd64 --scanners vuln \
  --format json --output .ci/image-scan.json
trivy image --input .ci/agent-candidate --platform linux/amd64 --scanners vuln \
  --severity HIGH,CRITICAL --ignore-unfixed --exit-code 1
} >> .ci/image-scan.log 2>&1
