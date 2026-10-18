#!/bin/sh
set -eu
mkdir -p .ci
trap 'cat .ci/shell-lint.log' EXIT
# All repository shell scripts live under scripts; the Alpine tool image has no Git.
find scripts -type f -name '*.sh' -exec \
  shellcheck --external-sources --source-path=SCRIPTDIR {} + > .ci/shell-lint.log 2>&1
