# GitLab CI

The pipeline lives in `.gitlab-ci.yml`. Branch and merge request pipelines run
the Go checks, Docker and rootless Podman execution contracts, the agent image
contract, and the pinned DurpDeploy server API and Chromium fleet UI contracts.
An open merge request suppresses duplicate branch push pipelines. Checkout
credentials are kept out of Git URLs and the mounted test checkout. Full history
preserves the commit-count build number. The server fixture stays pinned to
`488a91151f1aa02b46a3a47f406aeb003476181c` in the migrated GitLab repository.

## Runner and test environment

Use the privileged Linux amd64 Kubernetes executor. Jobs are untagged; the
current instance runner accepts them and has the `infra` tag. No native ARM64
runner is required. Each Docker job uses a separate `docker:dind` service in its
Kubernetes pod, accessed over pod-local `127.0.0.1:2375` with TLS disabled.
This is an ephemeral CI daemon, separate from deployed agent runtime sockets.

The prepare stage builds `.gitlab/ci/Dockerfile` and pushes a commit-specific
tools image to `$CI_REGISTRY_IMAGE/ci:$CI_COMMIT_SHA` with the job's GitLab
registry credentials. Check jobs run that image as a privileged disposable
systemd container inside their own DinD service. This keeps sockets and temporary
bind mounts beside the test processes. The non-root `ci` user has subordinate
UID/GID ranges, a systemd user manager with delegated CPU/memory/PID controllers,
and VFS storage for nested rootless Podman. Its root-owned mapping helpers use
only the respective `cap_setuid` and `cap_setgid` file capabilities, with setuid
bits cleared. User namespaces and writable nested
cgroup v2 must be supported by the runner's nodes. Runtime preflight and the live
resource contracts enforce these requirements. The environment is removed on
completion, failure, or cancellation; the Kubernetes job pod also owns the DinD
service. This does not require a host socket or a runner configuration change.

`container-podman` is required after successful live validation of the runner's
user namespaces and delegated controllers. Docker and Podman execution/API
failures block the pipeline and publication. Image hardening is independently
enforced by the required `image-amd64` job.

Go compilation caches are scoped to the branch. Environment and daemon logs
are retained for one week, including on failure. The version job also uploads
Chromium screenshots under `.ci/agent-version-ui/`.

## Static analysis, vulnerabilities, and fuzzing

Three additional blocking jobs run directly in Harbor-proxied Go/tool images;
they do not need Docker or rootless Podman. Image publication waits for all three.

- `lint` uses golangci-lint `v2.15.0` with its standard linters, including
  staticcheck and errcheck. The full repository must pass; there is no baseline
  exception. Run `bash scripts/ci/go-check.sh lint` locally with that version
  installed, or use its Harbor-proxied image.
- `vulnerability` runs govulncheck `v1.9.0` against `./...` and the Go standard
  library using the job's Go `1.26.9` toolchain and the current Go vulnerability
  database. Reachable vulnerabilities or scan errors fail the job. Run
  `bash scripts/ci/go-check.sh vulnerability` locally with Go 1.26 or newer.
- `fuzz` runs 30 seconds per target with two workers: protocol decoding,
  ZIP extraction, and encrypted payload opening. It checks strict JSON
  boundaries, staging confinement/cleanup, and recipient/deployment binding
  and tamper rejection. Inputs and archive expansion are bounded for CI;
  existing unit tests cover production size limits. Run
  `bash scripts/ci/go-check.sh fuzz` locally. Seed cases also run with `go test`.

Logs and any failing fuzz corpus entries under each package's `testdata/fuzz/`
are retained for one week. Keep useful regression corpus entries in Git after
fixing a failure. The regular Go check still runs vet, race tests, and `make check`.

`shell-lint` uses Harbor's pinned ShellCheck Alpine `v0.11.0` image for all shell
scripts under `scripts/`, following their sourced files. The two inline
exceptions document literal shell/documentation text rather than disabling a
rule repository-wide. `secrets` uses Harbor's pinned Gitleaks `v8.30.1` image.
It scans every MR commit from the supplied diff base, or a branch's before/after
range; tags and initial pushes scan the tip commit's patch. Repository/range
errors and scanner errors fail. Reports/logs redact detected values. Both jobs
block publication. No private-key allowlist or historical baseline is installed;
an audit of all older history remains a separate operation from this change gate.
The Python CI helpers have unit coverage alongside the Go report. Python test
files are classified as tests, while the helpers stay in measured sources.

## GitLab test results and coverage

Native reports work with GitLab Community Edition/Free. The `test` job writes
JUnit results from its existing race, unit and subprocess runs. The always-enabled
`coverage` job writes JUnit results from its existing full Go suite and Python tests, using
pinned gotestsum `v1.13.0` and Fedora's pytest. CI-only Go tools and their
transitive dependencies are locked in `.gitlab/ci/go.mod` and `go.sum`, and
installed with `-mod=readonly`. Report generation reuses the test
executions in those jobs; coverage also runs on refs where SonarCloud is omitted.
Race, unit, subprocess, Go coverage, and Python report names keep repeated test
cases distinct, so GitLab does not discard a later failure as a duplicate.

The coverage job exports Go and Python Cobertura reports with repository-relative
filenames for line annotations in MR diffs. Its Go statement percentage also
appears in GitLab's coverage summary. Go conversion uses gocover-cobertura pinned
to `4afa1205ab3b54ae098dd4724c1657aad10f7484` with strict package resolution.
The original `coverage.out` and `.ci/coverage-python.xml` remain SonarCloud inputs.
SonarCloud is optional; native reports and the coverage publication gate do not
depend on its token.

Reports are uploaded even when tests fail and retained for one week. Test
failures still return nonzero; report conversion failures also fail the job.
Open the pipeline's **Tests** tab or the MR's test summary for individual Go and
Python results. Coverage annotations appear on changed source lines after the
pipeline finishes processing. The other container, service, and browser jobs
retain their detailed logs and screenshots.

## Live amd64 service check

`systemd-amd64` installs the native binary and unmodified service unit inside its
disposable privileged systemd tools container. The agent runs as its dedicated
unprivileged service user. A loopback pinned-mTLS fixture pairs it, observes a
poll, checks the real process UID/GID, zero capability sets and no-new-privileges,
and reads the live cgroup's CPU, memory and PID limits. A graceful stop/start must
resume polling against the same pinned endpoint with unchanged durable state,
certificate/key hashes and permissions. The restarted fixture uses a fresh
listener/channel so old polls cannot satisfy the assertion. Both stops require
a zero exit status and no live MainPID. This job is required for publication.
Run `scripts/ci/check-systemd.sh` only inside the disposable amd64 CI tools
container as root with `CI=true`; it installs into that container's filesystem.
Pairing codes are used only in memory and redacted from retained journal logs.

## Required amd64 image and upgrade checks

`image-amd64` first builds one amd64/arm64 OCI layout under
`.ci/agent-candidate/`. Go cross-compiles from the native builder; official
BuildKit's bundled QEMU helpers handle ARM runtime package installation without
modifying host binfmt registration. Skopeo imports the amd64 variant into Docker;
its image config digest must equal the candidate's config digest before testing.
The job runs the complete image-hardening contract through Docker,
including UID 10001, zero capabilities, no new privileges, a read-only root,
writable private state, CPU/memory/PID limits, and stamped build identity.
`AGENT_CONTAINER_CONTRACT_RUNTIME=docker make container-contract` reproduces
the runtime choice; local `make container-contract` still defaults to Podman.
The current runtime base is Alpine 3.24.

The same job rebuilds the pinned `v0.1.1` agent source at
`32a7c5575b0985960f12581385361894061ad29f` as a previous-version fixture. Only its
upstream image addresses are rewritten to Harbor; its original runtime base and
agent source are retained. It pairs that image with a temporary control-plane
fixture over pinned mTLS, observes its poll, and stops it gracefully. The current
image then starts on the same private state volume and must poll with its new
build identity using the original client certificate and server pin. Hashes and
permissions of the state, certificate, and key must remain unchanged. No second
pairing request is sent. The fixture binds only loopback; its agent containers
use the disposable daemon's host network to reach it. Test containers, volumes,
and temporary server keys are cleaned up without logging pairing codes or keys.
To run that test locally, set `AGENT_UPGRADE_PREVIOUS_IMAGE` and
`AGENT_UPGRADE_CANDIDATE_IMAGE` to existing images, select
`AGENT_TEST_RUNTIME=docker` or `podman`, and run
`go test -count=1 -v -run '^TestUpgradeImages$' ./scripts/ci/image-upgrade`.
Ordinary Go test runs skip the image integration test when neither image is set.

After hardening and the upgrade pass, `.ci/image-verified.json` binds their
runtime image config to both platform manifests and the multi-platform index.
The verifier hashes all referenced blobs. `make check` includes corruption,
wrong-runtime, stale-proof, wrong-commit and missing/duplicate-platform cases.

`image-scan` scans the amd64 variant of that exact OCI layout with Trivy
`0.76.0`. Both the scanner and vulnerability database come through Harbor's
`github` proxy. The full JSON report and CycloneDX SBOM include the image's OS
packages and detected Go binaries. Any HIGH or CRITICAL vulnerability with an
available fix fails the job; unfixed findings remain visible in the full report.
Database, parsing, and scan errors also fail it. Image publication requires both
jobs. The OCI layout and verification proof expire after one day; scan reports
and SBOMs after one week.

These new checks run only on Linux amd64. Existing amd64/arm64 publication and
cross-compiled release binaries remain unchanged. No scheduled nightly jobs are
configured.

## Registries and releases

All upstream CI, service, Dockerfile, BuildKit, emulation, and contract test
images are fetched through `harbor.durp.info`: `dockerhub` for Docker Hub,
`microsoft` for MCR, `github` for Trivy and its database, and `gitlab` for GitLab's
CLI. The remaining `forgejo`, `k8s`, and `quay` proxies are available when an image from those
registries is needed. Test fixtures retain their public image names by tagging
Harbor-fetched images locally before the executors inspect them. Podman also
remaps Docker Hub through `registries.conf`. The isolated Docker daemon uses a
loopback-only, pull-only registry mirror that forwards requests and anonymous
Harbor token scopes to the `dockerhub` project. This preserves canonical
Docker Hub digest references for the agent's pinned Alpine staging helper.
The mirror never receives GitLab credentials. The proxy
projects must permit the runner to pull; private Harbor projects require
runner image pull credentials and Docker/Podman authentication.

The application image is published to `$CI_REGISTRY_IMAGE`, currently
`registry.durp.info/durfy/apps/durp-deploy-agent`, only for the default branch
and valid semantic `v*` tags after the required checks. Version, short-SHA, and
`latest` tags retain their existing meanings. Registry publication is serialized.
Publication validates the OCI layout against its runtime proof and current build
metadata, then Skopeo copies all manifests/blobs with `--preserve-digests`.
Every resulting tag's raw index digest must match the verified candidate.
There is no publication rebuild. The ARM image and provenance remain in the
same index; new runtime tests and vulnerability gating target amd64.
Merge request pipelines push only their CI tools image to the registry. The
multi-platform candidate remains a short-lived CI artifact; application registry
tags and releases remain limited to the default branch and semantic tags.

Buildx uses the legacy attestation format for registry compatibility, retaining
provenance. `BUILDX_NO_DEFAULT_OCI_ARTIFACT=1` avoids GitLab rejecting a new
image's OCI artifact attestation with `MANIFEST_BLOB_UNKNOWN`.

Tag pipelines cross-compile Linux amd64 and arm64 binaries and verify the native
binary's stamped identity. After checks and image publication succeed, `glab`
creates the GitLab release with permanent generic-package download assets using
`CI_JOB_TOKEN`. No GitHub token or GHCR credentials are needed.

## Optional SonarCloud

The existing `sonar-project.properties` project key and organization are kept.
Add a masked `SONAR_TOKEN` CI/CD variable and configure the project's GitLab
binding in SonarCloud to enable its quality-gate job. A protected
token is available only on refs allowed by GitLab's protected-variable policy.
Analysis runs on the default branch and same-project merge requests when the
token is available. Fork merge requests do not receive this job. A missing
token omits the SonarCloud job; an enabled scan's quality-gate failure fails the pipeline
and blocks image publication. The publisher uses an optional dependency on
`sonarcloud`: absence is permitted, but a present job must pass.
