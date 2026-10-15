#!/usr/bin/env bash
# Pass normal go build flags, such as -o PATH or -trimpath, as arguments.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/build_metadata.sh"
package=github.com/DeveloperDurp/durpdeploy-agent/internal/buildinfo
ldflags="-s -w -X $package.version=$BUILD_VERSION"
ldflags+=" -X $package.commit=$BUILD_COMMIT"
ldflags+=" -X $package.buildNumber=$BUILD_NUMBER"
ldflags+=" -X $package.modified=$BUILD_MODIFIED"
exec go build -ldflags "$ldflags" "$@" ./cmd/agent
