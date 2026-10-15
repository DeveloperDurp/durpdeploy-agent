# Software versions and build numbers

Agent builds use the same metadata and version format as DurpDeploy.
Check an executable without runtime configuration:

```bash
durpdeploy-agent version
durpdeploy-agent --version
durpdeploy-agent -v
```

These commands exit successfully without pairing, polling, or acquiring the
state lease. They can run while the installed service is running.

```text
durpdeploy-agent v1.2.3 (build 742, commit 0123456789abcdef0123456789abcdef01234567)
```

## Build identity

`scripts/build_metadata.sh` supplies the software version, build number, full
source commit, and source-change status. `scripts/build_agent.sh` embeds those
values with Go linker flags. `make build`, `make container`, CI images, and
standalone release binaries use these scripts.

The build number is `git rev-list --count HEAD`, including reachable merge
commits. Rebuilding the same source keeps that count. Different branches can
have the same count; use the source commit to identify an exact build. Rewritten
history can change the count. CI fetches full history before computing metadata.

A clean checkout at an exact semantic release tag uses that tag, including its
leading `v`, prerelease suffix, and build metadata. Tag publication validates
SemVer and refuses reported source changes. Untagged CI builds use:

```text
v0.0.0-dev.742+g0123456789ab
```

Local changes add `.dirty` to this development version and `, modified` to the
diagnostic output. A dirty tagged checkout is a development build. A shallow
checkout without a known commit count reports `dev` and an unknown build number,
with the commit when available. Source archives without Git metadata report
`dev`, an unknown build number, and an unknown commit unless metadata is supplied.

Plain `go build ./cmd/agent` also reports `dev` with an unknown build number. It
uses Go's embedded VCS revision and modification status when available. Use
`make build` for the full stamped identity.

## Containers and release binaries

`make container` forwards the metadata to the Dockerfile. A direct Docker build
can use the same inputs from a Bash shell:

```bash
source scripts/build_metadata.sh
docker build --build-arg BUILD_VERSION="$BUILD_VERSION" \
  --build-arg BUILD_NUMBER="$BUILD_NUMBER" \
  --build-arg BUILD_COMMIT="$BUILD_COMMIT" \
  --build-arg BUILD_MODIFIED="$BUILD_MODIFIED" \
  -t localhost/durpdeploy-agent:local .
docker run --rm localhost/durpdeploy-agent:local version
```

Bare Docker/Compose builds have no Git context and default to `dev` with unknown
commit and build number. Supply build arguments for a known identity.
Image labels record the software version, revision, and build number as
`org.opencontainers.image.version`, `org.opencontainers.image.revision`, and
`info.durpdeploy.build-number`.

CI publishes the generated development version or release tag alongside
`latest`; existing short-SHA tags on `main` remain available. OCI tags replace
`+` with `_` because OCI tags cannot contain `+`. The executable and version
label preserve the original semantic version. Both Linux release architectures
receive the same version, source commit, and build number.

## Reported versions and upgrades

With `DURPDEPLOY_AGENT_VERSION` unset or empty, pairing and every poll report the
embedded build string, without the `durpdeploy-agent` program-name prefix. The
fleet API and Agents list/detail pages display this value through the existing
`agent_version` contract. A compatible restart reports the new executable's
identity even if pairing state records an older version; re-pairing is unnecessary.

`DURPDEPLOY_AGENT_VERSION` remains an optional operator override for the reported
value. It does not change embedded metadata. Version commands print it separately
as `Reported agent version override`, and startup logs include both `build` and
`reported_version`. Remove an old override if fleet diagnostics show an unexpected
version after upgrading. Overrides are operator reports, not proof of executable
identity.

Before and after installing an upgrade, run the installed executable's `version`
command and compare its source commit with the intended artifact. Preserve paired
identity, pins, and state across compatible upgrades and rollbacks.

Software versions do not replace `agent/1`, `agent/2`, `agent/3`, `agent-v1`, or
capability reports. Compatibility continues to use the existing protocol and
capability negotiation. Automatic upgrades and release-number increments are
outside this mechanism.

Run `make build-version-test` for tagged/development executable and metadata
checks. The coordinated CI contract in `scripts/check-agent-version-e2e.sh`
uses the real agent binary and the pinned DurpDeploy server to verify pairing,
polling, fleet API values, and Chromium-rendered agent pages at desktop and mobile
widths for defaults and overrides.
