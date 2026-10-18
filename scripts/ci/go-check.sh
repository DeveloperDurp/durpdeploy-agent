#!/usr/bin/env bash
set -euo pipefail
mkdir -p .ci

case "${1:?}" in
  lint)
    golangci-lint run --timeout=5m 2>&1 | tee .ci/lint.log
    ;;
  vulnerability)
    # Text mode exits nonzero for reachable vulnerabilities (JSON/SARIF do not).
    go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./... 2>&1 | tee .ci/vulnerability.log
    ;;
  fuzz)
    for target in protocol:FuzzDecodeRequest artifact:FuzzExtractZIP payload:FuzzOpenEnvelope; do
      package=${target%%:*}
      name=${target#*:}
      go test "./$package" -run='^$' -fuzz="^$name\$" \
        -fuzztime=30s -parallel=2 -timeout=3m 2>&1 | tee ".ci/fuzz-$package.log"
    done
    ;;
  *) printf 'Unknown Go check: %s\n' "$1" >&2; exit 1 ;;
esac
