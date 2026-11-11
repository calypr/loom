#!/usr/bin/env bash
set -euo pipefail

# Keep the dataframe pipeline directional. This is intentionally a small
# source check rather than a Go test so it can run before compilation in CI.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The whole check is rg-powered. Without a guard an absent rg made every
# rule exit non-match and the script passed vacuously (arch-backend-07 F1
# lesson: a guard that cannot RED is not a guard), so a missing or broken
# rg is now a hard failure, never a silent pass.
if ! command -v rg >/dev/null 2>&1; then
  echo "dataframe package boundary check requires rg; refusing to pass vacuously (install ripgrep)" >&2
  exit 1
fi

# Per-rule allowlist of repo-relative files whose matches are tolerated.
# Interim allowlist added by arch-backend-07 Wave B (2026-11-11) per the
# scripts-lane ruling: the entry is pre-existing, predates Wave B, and its
# permanent policy (fix the import vs. allow as a shared leaf) is a
# registered scripts-lane follow-up. Without it, the new fail-loud guard
# would weaponize `make test` for every rg-having developer over an import
# this ticket never touched. Do not add entries without a scripts-lane
# ruling.
declare -A BOUNDARY_ALLOWLIST=(
  # internal/dataframe/spec/grain.go imports internal/dataframe/errors:
  # pre-existing, predates Wave B, policy follow-up.
  ["spec imports runtime/compiler implementation"]="internal/dataframe/spec/grain.go"
)

fail_if_matches() {
  local label="$1"
  local pattern="$2"
  shift 2
  local matches="" rc=0 entry
  matches="$(rg -n --glob '*.go' --glob '!**/*_test.go' "$pattern" "$@")" || rc=$?
  if (( rc > 1 )); then
    echo "dataframe package boundary check failed to run: $label (rg exit $rc)" >&2
    exit 1
  fi
  if [[ -z "$matches" ]]; then
    return 0
  fi
  # ripgrep applies --glob filters during traversal only: files named
  # explicitly as arguments (the recipe rule's shell-expanded recipe/*.go)
  # are searched regardless. Drop test files by path so the documented
  # production-only intent holds for every rule shape, not just the
  # directory-shaped ones.
  # Post-filter greps fail loud like rg: rc>1 means grep could not run
  # (missing binary, bad pattern), which must never read as convergence.
  # Only rc==1 (everything filtered out) with empty output converges.
  local filtered="" grc=0
  filtered="$(printf '%s\n' "$matches" | grep -v -E '^[^:]+_test\.go:')" || grc=$?
  if (( grc > 1 )); then
    echo "dataframe package boundary check failed to run: $label (grep exit $grc)" >&2
    exit 1
  fi
  matches="$filtered"
  if [[ -z "$matches" ]]; then
    return 0
  fi
  for entry in ${BOUNDARY_ALLOWLIST[$label]:-}; do
    filtered="" grc=0
    filtered="$(printf '%s\n' "$matches" | grep -v -F -- "$root/$entry")" || grc=$?
    if (( grc > 1 )); then
      echo "dataframe package boundary check failed to run: $label (grep exit $grc)" >&2
      exit 1
    fi
    matches="$filtered"
  done
  if [[ -n "$matches" ]]; then
    printf '%s\n' "$matches" >&2
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

# arch-backend-07 Wave B: the adapter tree moved from internal/explorer/arango
# (and the other retired adapter paths) under internal/backends/arango, so the
# ban now names internal/backends/ — otherwise this rule would have gone
# vacuous on the very tree it exists to forbid.
fail_if_matches "authoring intent imports orchestration or storage" \
  'internal/(server|store|backends|explorer/(lifecycle|compilation))|internal/dataframe/(execution|materialization|publication|published)' \
  "$root/internal/explorer/authoringv2"

echo "dataframe package boundaries: ok"
