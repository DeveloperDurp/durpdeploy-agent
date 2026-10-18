#!/bin/sh
set -eu
mkdir -p .ci
docker buildx create --name agent --driver docker-container \
  --driver-opt image=harbor.durp.info/dockerhub/moby/buildkit:buildx-stable-1 --use
trap 'docker buildx rm agent' EXIT
# Official BuildKit carries its own QEMU helpers; no host binfmt change is needed.
docker buildx build --platform linux/amd64,linux/arm64 \
  --output type=oci,dest=.ci/agent-candidate,tar=false \
  --build-arg BUILD_VERSION --build-arg BUILD_NUMBER \
  --build-arg BUILD_COMMIT --build-arg BUILD_MODIFIED \
  --label "org.opencontainers.image.source=${CI_PROJECT_URL:-https://gitlab.durp.info/durfy/apps/durp-deploy-agent}" .
python3 scripts/ci/image-artifact.py describe > .ci/image-built.json
