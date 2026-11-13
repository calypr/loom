#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v go >/dev/null 2>&1; then
  echo "dataframe package boundary check requires go; refusing to pass vacuously" >&2
  exit 1
fi

if ! command -v rg >/dev/null 2>&1; then
  echo "dataframe package boundary check requires rg; refusing to pass vacuously (install ripgrep)" >&2
  exit 1
fi

(cd "$root" && go run ./scripts/check-boundaries --self-test)

(cd "$root" && go run ./scripts/check-boundaries --root "$root")

require_dir() {
  local label="$1" dir="$2"
  if [[ ! -d "$dir" ]]; then
    echo "dataframe package boundary check target missing: $label ($dir)" >&2
    exit 1
  fi
}

fail_if_matches() {
  local label="$1"
  local pattern="$2"
  shift 2
  require_dir "$label" "$1"
  local matches="" rc=0 entry
  matches="$(rg -n --glob '*.go' --glob '!**/*_test.go' "$pattern" "$@")" || rc=$?
  if (( rc > 1 )); then
    echo "dataframe package boundary check failed to run: $label (rg exit $rc)" >&2
    exit 1
  fi
  if [[ -z "$matches" ]]; then
    return 0
  fi
  local filtered="" grc=0
  filtered="$(printf '%s\n' "$matches" | grep -v -E '^[^:]+_test\.go:')" || grc=$?
  if (( grc > 1 )); then
    echo "dataframe package boundary check failed to run: $label (grep exit $grc)" >&2
    exit 1
  fi
  matches="$filtered"
  if [[ -n "$matches" ]]; then
    printf '%s\n' "$matches" >&2
    echo "dataframe package boundary violation: $label" >&2
    exit 1
  fi
}

fail_if_matches "optimizer references storage-route resolution or the schema package" \
  'ResolveStorageRoute|fhirschema' \
  "$root/internal/dataframe/compiler/optimize"

fail_if_matches "AQL renderer references storage-route resolution" \
  'ResolveStorageRoute' \
  "$root/internal/dataframe/compiler/render/aql"

echo "dataframe package boundaries: ok"
