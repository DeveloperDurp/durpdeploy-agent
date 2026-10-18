#!/usr/bin/env bash
# Verify real Git tag, history, dirty-tree, shallow-clone, and archive behavior.
set -euo pipefail
script=$(cd "$(dirname "$0")" && pwd)/build_metadata.sh
directory=$(mktemp -d)
trap 'rm -rf "$directory"' EXIT
unset BUILD_VERSION BUILD_NUMBER BUILD_COMMIT BUILD_MODIFIED GITHUB_REF_TYPE GITHUB_REF_NAME
unset CI_COMMIT_TAG GITLAB_CI
git init --quiet --initial-branch=fixture "$directory/source"
cd "$directory/source"
printf source > tracked
git add tracked
git -c user.name=Fixture -c user.email=fixture@example.test commit --quiet -m Source
commit=$(git rev-parse HEAD)

expect() {
	local output=$1 expected=$2
	if ! grep -Fxq -- "$expected" <<< "$output"; then
		printf 'Expected %s in metadata:\n%s\n' "$expected" "$output" >&2
		exit 1
	fi
}

output=$(bash "$script")
expect "$output" "software_version=v0.0.0-dev.1+g${commit:0:12}"
expect "$output" "commit=$commit"
expect "$output" build_number=1
git tag v1.2.3+build.4
output=$(bash "$script")
expect "$output" software_version=v1.2.3+build.4
expect "$output" version=v1.2.3_build.4
output=$(GITHUB_REF_TYPE=branch bash "$script")
expect "$output" "software_version=v0.0.0-dev.1+g${commit:0:12}"
output=$(GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v1.2.3+build.4 bash "$script")
expect "$output" is_release=true
output=$(GITLAB_CI=true bash "$script")
expect "$output" "software_version=v0.0.0-dev.1+g${commit:0:12}"
expect "$output" is_release=false
output=$(GITLAB_CI=true CI_COMMIT_TAG=v1.2.3+build.4 bash "$script")
expect "$output" software_version=v1.2.3+build.4
expect "$output" version=v1.2.3_build.4
expect "$output" is_release=true
if GITLAB_CI=true CI_COMMIT_TAG=v01.2.3 bash "$script" >/dev/null 2>&1; then
	echo 'Accepted invalid GitLab semantic release tag' >&2
	exit 1
fi
if GITLAB_CI=true CI_COMMIT_TAG=v1.2.3 BUILD_VERSION=v2.0.0 bash "$script" >/dev/null 2>&1; then
	echo 'Accepted a build version that differs from the GitLab release tag' >&2
	exit 1
fi
if GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v01.2.3 bash "$script" >/dev/null 2>&1; then
	echo 'Accepted invalid semantic release tag' >&2
	exit 1
fi
printf dirty >> tracked
if GITLAB_CI=true CI_COMMIT_TAG=v1.2.3 bash "$script" >/dev/null 2>&1; then
	echo 'Accepted dirty source as a GitLab release' >&2
	exit 1
fi
output=$(bash "$script")
expect "$output" "software_version=v0.0.0-dev.1+g${commit:0:12}.dirty"
expect "$output" modified=true
if GITHUB_REF_TYPE=tag GITHUB_REF_NAME=v1.2.3 bash "$script" >/dev/null 2>&1; then
	echo 'Accepted dirty source as a release' >&2
	exit 1
fi
git restore tracked
printf extra > untracked
expect "$(bash "$script")" modified=true
rm untracked
git clone --quiet --depth 1 "file://$directory/source" "$directory/shallow"
cd "$directory/shallow"
output=$(GITHUB_REF_TYPE=branch bash "$script")
expect "$output" software_version=dev
expect "$output" build_number=unknown
expect "$output" "commit=$commit"
mkdir "$directory/archive"
cd "$directory/archive"
output=$(bash "$script")
expect "$output" software_version=dev
expect "$output" commit=unknown
expect "$output" build_number=unknown
echo 'PASS: tagged, development, dirty, shallow, and archive build metadata'
