#!/bin/sh
# Own the environment and its descendants for this job, including cancellation.
set -eu
kind=${1:?usage: run.sh test|docker|podman|image-amd64|systemd|version-e2e|coverage|release}
name=durpdeploy-ci-${CI_JOB_ID:?}
mkdir -p .ci .cache/go .cache/mod
cleanup() {
  status=$?
  trap - EXIT HUP INT TERM
  docker logs "$name" 2>&1 | \
    sed -E 's/(Pairing code: ).*/\1[REDACTED]/' > .ci/environment.log || true
  # The live service emits its short-lived pairing code to the journal.
  docker exec "$name" journalctl --no-pager -b -n 200 2>&1 | \
    sed -E 's/(Pairing code: ).*/\1[REDACTED]/' >> .ci/environment.log || true
  docker rm --force "$name" >/dev/null || status=1
  exit "$status"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
docker run --detach --name "$name" --privileged --cgroupns=private \
  --tmpfs /run --tmpfs /run/lock --tmpfs /tmp:exec,mode=1777 \
  --volume "$CI_PROJECT_DIR:/workspace" "$CI_TOOLS_IMAGE"
attempt=0
until docker exec "$name" systemctl is-active --quiet multi-user.target; do
  attempt=$((attempt + 1))
  [ "$attempt" -lt 60 ] || exit 1
  sleep 1
done
docker exec "$name" systemctl start user@1000.service
docker exec "$name" chown -R 1000:1000 /workspace
if [ "$kind" = docker ] || [ "$kind" = version-e2e ] || [ "$kind" = image-amd64 ]; then
  docker exec "$name" bash scripts/ci/start-docker.sh
fi
user=ci
if [ "$kind" = systemd ]; then user=root; fi
docker exec \
  --env CI=true --env GITLAB_CI=true --env CI_COMMIT_TAG --env CI_COMMIT_SHA \
  --env CI_PROJECT_URL \
  --env BUILDX_NO_DEFAULT_OCI_ARTIFACT \
  --env BUILD_VERSION --env BUILD_NUMBER --env BUILD_COMMIT --env BUILD_MODIFIED \
  --env SERVER_REPOSITORY --env SERVER_COMMIT \
  "$name" runuser -u "$user" -- env \
    XDG_RUNTIME_DIR=/run/user/1000 \
    DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1000/bus \
    GOCACHE=/workspace/.cache/go GOMODCACHE=/workspace/.cache/mod \
    bash scripts/ci/check.sh "$kind"
