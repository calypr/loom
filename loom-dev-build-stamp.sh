#!/usr/bin/env sh
set -eu

source_digest() {
  # The source digest includes file contents and names, so edits and deletions
  # cannot be mistaken for the prior process merely because a timestamp is
  # unchanged.
  {
  sha256sum /workspace/go.mod /workspace/go.sum
  find /workspace/cmd /workspace/internal /workspace/generated /workspace/schemas \
    -type f ! -name '*_test.go' ! -path '*/testdata/*' -print0 2>/dev/null \
    | sort -z \
    | xargs -0 sha256sum
  } | sha256sum | awk '{print $1}'
}

if [ "${1:-}" = "--source" ]; then
  source_digest
  exit
fi

stamp_path=/workspace/tmp/arango-fhir-server.build.json
if [ "${1:-}" = "--record" ]; then
  before_source="${2:-}"
  after_source="$(source_digest)"
  if [ -z "$before_source" ] || [ "$before_source" != "$after_source" ]; then
    printf 'source changed during Go build (before=%s after=%s)\n' "$before_source" "$after_source" >&2
    exit 1
  fi
  binary_digest="$(sha256sum /workspace/tmp/arango-fhir-server | awk '{print $1}')"
  printf '{"source":"%s","binary":"%s"}\n' "$after_source" "$binary_digest" > "$stamp_path"
  exit
fi

if [ "${1:-}" = "--check" ]; then
  source_digest="$(source_digest)"
  binary_digest="$(sha256sum /workspace/tmp/arango-fhir-server | awk '{print $1}')"
  expected_source="$(sed -n 's/.*"source":"\([a-f0-9]*\)".*/\1/p' "$stamp_path")"
  expected_binary="$(sed -n 's/.*"binary":"\([a-f0-9]*\)".*/\1/p' "$stamp_path")"
  running_binary=''
  for executable in /proc/[0-9]*/exe; do
    if [ "$(readlink "$executable" 2>/dev/null || true)" = /workspace/tmp/arango-fhir-server ]; then
      running_binary="$(sha256sum "$executable" | awk '{print $1}')"
      break
    fi
  done
  printf '%s %s %s\n' "$expected_source" "$source_digest" "$running_binary"
  [ "$expected_source" = "$source_digest" ] && [ "$expected_binary" = "$binary_digest" ] && [ "$running_binary" = "$binary_digest" ]
  exit
fi
printf 'usage: %s --source | --record SOURCE_DIGEST | --check\n' "$0" >&2
exit 2
