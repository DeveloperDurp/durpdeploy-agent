#!/bin/sh
set -eu
[ "${GITLAB_CI:-}" = true ]
bash scripts/build_metadata.sh > /dev/null
version=$(printf '%s' "${BUILD_VERSION:?}" | tr + _)
[ "${IMAGE_VERSION:?}" = "$version" ]
if [ -z "${CI_COMMIT_TAG:-}" ]; then
  [ "${CI_COMMIT_BRANCH:?}" = "${CI_DEFAULT_BRANCH:?}" ]
fi
expected=$(python3 scripts/ci/image-artifact.py verify-proof)
auth=$(mktemp)
trap 'rm -f "$auth"' EXIT
printf '%s' "${CI_REGISTRY_PASSWORD:?}" | skopeo login --authfile "$auth" \
  --username "${CI_REGISTRY_USER:?}" --password-stdin "${CI_REGISTRY:?}"
set -- "${CI_REGISTRY_IMAGE:?}:${IMAGE_VERSION:?}" "$CI_REGISTRY_IMAGE:latest"
if [ "${CI_COMMIT_BRANCH:-}" = "$CI_DEFAULT_BRANCH" ]; then
  short_sha=$(printf '%s' "$CI_COMMIT_SHA" | cut -c1-7)
  set -- "$@" "$CI_REGISTRY_IMAGE:$short_sha"
fi
for image in "$@"; do
  skopeo copy --authfile "$auth" --all --preserve-digests \
    oci:.ci/agent-candidate "docker://$image"
  actual=$(skopeo inspect --authfile "$auth" --raw "docker://$image" | sha256sum)
  [ "${actual%% *}" = "${expected#sha256:}" ]
done
