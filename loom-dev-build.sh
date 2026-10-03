#!/usr/bin/env sh
set -eu

while :; do
  before_source="$(/workspace/loom-dev-build-stamp.sh --source)"
  build_status=0
  go build -mod=readonly -o ./tmp/arango-fhir-server ./cmd/arango-fhir-server || build_status=$?
  after_source="$(/workspace/loom-dev-build-stamp.sh --source)"
  if [ "$before_source" != "$after_source" ]; then
    printf 'source changed during build; rebuilding current source\n' >&2
    continue
  fi
  [ "$build_status" -eq 0 ] || exit "$build_status"
  if /workspace/loom-dev-build-stamp.sh --record "$before_source"; then
    exit 0
  fi
  [ "$before_source" != "$(/workspace/loom-dev-build-stamp.sh --source)" ] || exit 1
done
