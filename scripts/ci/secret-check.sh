#!/bin/sh
set -eu
mkdir -p .ci
trap 'cat .ci/secrets.log' EXIT
: > .ci/secrets.log
git config --global --add safe.directory "$PWD"
head=${CI_COMMIT_SHA:-$(git rev-parse HEAD)}
base=${CI_MERGE_REQUEST_DIFF_BASE_SHA:-${CI_COMMIT_BEFORE_SHA:-}}
valid_sha() {
  case "$1" in *[!0-9a-f]*|'') return 1 ;; esac
  [ "${#1}" -eq 40 ] || [ "${#1}" -eq 64 ]
}
valid_sha "$head"
git cat-file -e "$head^{commit}"
case "$base" in
  # Tags/initial pushes have no previous pipeline SHA. Check the tip's patch;
  # merge requests always scan every commit since their supplied diff base.
  ''|0000000000000000000000000000000000000000) range=$head^! ;;
  *) valid_sha "$base"; git cat-file -e "$base^{commit}"; range=$base..$head ;;
esac
# Fail closed on repository/range errors: Gitleaks can otherwise report zero commits.
git log --no-ext-diff --no-textconv --full-history -p "$range" > /dev/null
gitleaks git --redact=100 --no-banner --no-color \
  --report-format=json --report-path=.ci/secrets.json \
  --log-opts="--no-ext-diff --no-textconv --full-history $range" . > .ci/secrets.log 2>&1
if grep -Eq '(^|[[:space:]])(ERR|FTL)([[:space:]]|$)' .ci/secrets.log; then
  exit 1
fi
