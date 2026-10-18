#!/usr/bin/env bash
# Exercise the real stamped executable with no runtime configuration.
set -euo pipefail
unset GITHUB_REF_TYPE GITHUB_REF_NAME
unset CI_COMMIT_TAG GITLAB_CI
directory=$(mktemp -d)
trap 'rm -rf "$directory"' EXIT
commit=$(git rev-parse HEAD)
number=$(git rev-list --count HEAD)
for version in v1.2.3+build.4 "v0.0.0-dev.${number}+g${commit:0:12}"; do
	BUILD_VERSION=$version BUILD_COMMIT=$commit BUILD_NUMBER=$number BUILD_MODIFIED=false \
		bash scripts/build_agent.sh -o "$directory/durpdeploy-agent"
	for argument in version --version -v; do
		output=$(cd "$directory" && env -i ./durpdeploy-agent "$argument")
		[[ "$output" == "durpdeploy-agent $version (build $number, commit $commit)" ]]
		printf 'PASS: %s: %s\n' "$argument" "$output"
	done
	[[ $(find "$directory" -mindepth 1 -maxdepth 1 | wc -l) -eq 1 ]]
done
go build -o "$directory/unstamped" ./cmd/agent
[[ "$(env -i "$directory/unstamped" version)" == 'durpdeploy-agent dev (build unknown, commit '* ]]
echo 'PASS: unstamped executable reports a development identity'
