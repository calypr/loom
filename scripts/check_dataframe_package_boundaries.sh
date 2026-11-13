#!/usr/bin/env bash
set -euo pipefail

# Keep the dataframe pipeline directional (arch-backend-09 form).
#
# The authoritative parsed-import boundary policy lives in
# scripts/packagepolicy (one rule table, one parsed-import scanner). This
# entrypoint invokes it twice — negative fixture self-test first, then the
# repository scan — so a guard that cannot go RED never passes (arch-07 F1
# lesson). Only rules that are NOT import facts remain here as explicitly
# distinct text rules (bare identifier references), each with a hard
# vacuity guard on its target directory.
#
# Make target `dataframe-boundaries` and the CI `go-unit` job both call
# this script; the Go twins in internal/dataframe/compiler delegate to the
# same scripts/packagepolicy implementation. Do not restate a rule table
# here; add rows to scripts/packagepolicy/rules.go.
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The parsed-import half needs the Go toolchain; without a guard an absent
# `go` would skip every import rule and pass vacuously.
if ! command -v go >/dev/null 2>&1; then
  echo "dataframe package boundary check requires go; refusing to pass vacuously" >&2
  exit 1
fi

# The distinct text rules below are rg-powered. Without a guard an absent
# rg made every rule exit non-match and the script passed vacuously
# (arch-backend-07 F1 lesson: a guard that cannot RED is not a guard), so
# a missing or broken rg is a hard failure, never a silent pass.
if ! command -v rg >/dev/null 2>&1; then
  echo "dataframe package boundary check requires rg; refusing to pass vacuously (install ripgrep)" >&2
  exit 1
fi

# 1) The parsed-import policy must first prove it can go RED on fixtures:
#    a real forbidden import is named with file, import, and allowed
#    owner; removal converges; a nonexistent path, an empty tree, and a
#    malformed file are verification failures, never detections.
(cd "$root" && go run ./scripts/check-boundaries --self-test)

# 2) The repository scan against the authoritative table.
(cd "$root" && go run ./scripts/check-boundaries --root "$root")

# fail_if_matches runs one explicitly-distinct TEXT rule (a bare
# identifier reference that is not expressible as an import fact).
#
# Every rule must have a real target: a missing target directory fails
# loudly instead of matching nothing and passing vacuously.
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
  # ripgrep applies --glob filters during traversal only: files named
  # explicitly as arguments are searched regardless. Drop test files by
  # path so the documented production-only intent holds. Post-filter
  # greps fail loud like rg: rc>1 means grep could not run, which must
  # never read as convergence; only rc==1 with empty output converges.
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

# (The former rg import rules — spec/semantic/ir/optimizer/renderer/
# recipe/publication/authoringv2 import bans and the compiler
# backends/drivers ban — moved into the authoritative parsed-import table
# in scripts/packagepolicy/rules.go; their interim allowlist entry
# (internal/dataframe/spec/grain.go) travels with its rule row per the
# scripts-lane ruling. These two rules stay here because they ban bare
# identifier references, not imports — explicitly distinct rules with no
# duplicate rule table.)
fail_if_matches "optimizer references storage-route resolution or the schema package" \
  'ResolveStorageRoute|fhirschema' \
  "$root/internal/dataframe/compiler/optimize"

fail_if_matches "AQL renderer references storage-route resolution" \
  'ResolveStorageRoute' \
  "$root/internal/dataframe/compiler/render/aql"

echo "dataframe package boundaries: ok"
