#!/usr/bin/env bash
set -euo pipefail
image=localhost/durpdeploy-agent:amd64-check
source scripts/build_metadata.sh
export BUILD_VERSION BUILD_COMMIT BUILD_NUMBER BUILD_MODIFIED
[[ $(docker info --format '{{.Architecture}}') =~ ^(amd64|x86_64)$ ]]
sh scripts/ci/build-image.sh
skopeo copy --override-os linux --override-arch amd64 --dest-daemon-host "${DOCKER_HOST:?}" \
  oci:.ci/agent-candidate "docker-daemon:$image"
python3 scripts/ci/image-artifact.py verify-runtime \
  "$(docker image inspect --format '{{.Id}}' "$image")" > /dev/null
AGENT_CONTAINER_CONTRACT_RUNTIME=docker AGENT_CONTAINER_CONTRACT_PREBUILT=true \
  make container-contract IMAGE="$image"
[[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image") == linux/amd64 ]]

# Immutable v0.1.1 source, rebuilt as an isolated upgrade fixture. Preserve its
# original runtime base while routing all upstream image pulls through Harbor.
previous_commit=32a7c5575b0985960f12581385361894061ad29f
previous_image=localhost/durpdeploy-agent:upgrade-v0.1.1
previous_dir=$(mktemp -d .ci/upgrade-source.XXXXXX)
trap 'rm -rf "$previous_dir"' EXIT
git archive "$previous_commit" | tar -x -C "$previous_dir"
sed -i \
  -e 's|^# syntax=docker/dockerfile:1$|# syntax=harbor.durp.info/dockerhub/docker/dockerfile:1|' \
  -e 's|docker.io/library/|harbor.durp.info/dockerhub/library/|g' \
  "$previous_dir/Dockerfile"
docker build --platform linux/amd64 \
  --build-arg BUILD_VERSION=v0.1.1 --build-arg BUILD_COMMIT="$previous_commit" \
  --build-arg BUILD_NUMBER="$(git rev-list --count "$previous_commit")" \
  --build-arg BUILD_MODIFIED=false --tag "$previous_image" "$previous_dir"
AGENT_TEST_RUNTIME=docker AGENT_UPGRADE_PREVIOUS_IMAGE="$previous_image" \
  AGENT_UPGRADE_CANDIDATE_IMAGE="$image" \
  go test -count=1 -v -run '^TestUpgradeImages$' ./scripts/ci/image-upgrade 2>&1 | tee .ci/upgrade-smoke.log

# Issue proof only after the complete hardening and upgrade checks succeed.
python3 scripts/ci/image-artifact.py verify-runtime \
  "$(docker image inspect --format '{{.Id}}' "$image")" > .ci/image-verified.json
