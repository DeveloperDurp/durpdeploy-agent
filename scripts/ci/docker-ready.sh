#!/bin/sh
set -eu
attempt=0
until docker info >/dev/null 2>&1; do
  attempt=$((attempt + 1))
  if [ "$attempt" -ge 60 ]; then
    docker info
    exit 1
  fi
  sleep 1
done
