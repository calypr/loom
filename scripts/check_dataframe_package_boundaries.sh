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
  'internal/dataframe/compiler/(lower|optimize|render)|internal/dataframe/execution|internal/backends/arango' \
  "$root/internal/dataframe/compiler/ir"

fail_if_matches "optimizer imports renderer, lowerer, runtime, or schema routes" \
  'internal/dataframe/compiler/(lower|render)|internal/dataframe/execution|ResolveStorageRoute|fhirschema' \
  "$root/internal/dataframe/compiler/optimize"

fail_if_matches "renderer imports lowerer, optimizer, runtime, or Arango" \
  'internal/dataframe/compiler/(lower|optimize)|internal/dataframe/execution|internal/backends/arango|ResolveStorageRoute' \
  "$root/internal/dataframe/compiler/render/aql"

# arch-backend-06 leaf law: the compiled command is the compiler's leaf
# output. No compiler file may reach a backend implementation package or a
# database driver; the only vendor-adjacent edge is adapter -> command.
# Go-side twin: internal/dataframe/compiler/backend_leaf_guard_test.go.
fail_if_matches "compiler imports backend implementations or database drivers" \
  'github.com/calypr/loom/internal/backends/|github.com/jackc/pgx|"github.com/arangodb' \
  "$root/internal/dataframe/compiler"

fail_if_matches "stored recipe domain imports execution or storage layers" \
  'internal/dataframe/(compiler|execution|materialization|publication|store)' \
  "$root"/internal/dataframe/recipe/*.go

fail_if_matches "publication targets import dataframe execution orchestration" \
  'internal/dataframe/execution|internal/dataframe/published|internal/dataframe/materialization' \
  "$root"/internal/dataframe/publication

fail_if_matches "authoring intent imports orchestration or storage" \
  'internal/(server|store|explorer/(arango|lifecycle|compilation))|internal/dataframe/(execution|materialization|publication|published)' \
  "$root/internal/explorer/authoringv2"

echo "dataframe package boundaries: ok"
