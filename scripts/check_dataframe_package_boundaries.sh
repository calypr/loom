#!/usr/bin/env bash
set -euo pipefail

# Keep the dataframe pipeline directional. This is intentionally a small
# source check rather than a Go test so it can run before compilation in CI.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

fail_if_matches() {
  local label="$1"
  local pattern="$2"
  shift 2
  if rg -n --glob '*.go' --glob '!**/*_test.go' "$pattern" "$@"; then
    echo "dataframe package boundary violation: $label" >&2
    exit 1
  fi
}

fail_if_matches "spec imports runtime/compiler implementation" \
  'internal/dataframe/(execution|compiler|semantic|materialization|template|errors)' \
  "$root/internal/dataframe/spec"

fail_if_matches "semantic imports physical compiler implementation" \
  'internal/dataframe/compiler/(ir|lower|optimize|render)' \
  "$root/internal/dataframe/semantic"

fail_if_matches "ir imports lowering, optimization, rendering, or runtime" \
  'internal/dataframe/compiler/(lower|optimize|render)|internal/dataframe/execution|internal/store/arango' \
  "$root/internal/dataframe/compiler/ir"

fail_if_matches "optimizer imports renderer, lowerer, runtime, or schema routes" \
  'internal/dataframe/compiler/(lower|render)|internal/dataframe/execution|ResolveStorageRoute|fhirschema' \
  "$root/internal/dataframe/compiler/optimize"

fail_if_matches "renderer imports lowerer, optimizer, runtime, or Arango" \
  'internal/dataframe/compiler/(lower|optimize)|internal/dataframe/execution|internal/store/arango|ResolveStorageRoute' \
  "$root/internal/dataframe/compiler/render/aql"

fail_if_matches "stored recipe domain imports execution or storage layers" \
  'internal/dataframe/(compiler|execution|materialization|publication|store)' \
  "$root"/internal/dataframe/recipe/*.go

fail_if_matches "publication targets import dataframe execution orchestration" \
  'internal/dataframe/execution|internal/dataframe/published|internal/dataframe/materialization' \
  "$root"/internal/dataframe/publication

echo "dataframe package boundaries: ok"
