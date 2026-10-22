#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export LOOM_CDA_MINI_FIXTURE=1
export LOOM_DEV_FIXTURE_DIR="${LOOM_DEV_FIXTURE_DIR:-$ROOT/testdata/cda-mini-fixture}"

ARTIFACT_DIR="${LOOM_CDA_MINI_ARTIFACTS:-$ROOT/.artifacts/builder-journey/cda-mini}"
MANIFEST="$ROOT/verification/builder-interactions/cda-mini-journeys.json"
mkdir -p "$ARTIFACT_DIR"

journeys=(
  load-preview
  add-column-preview
  filter-preview
  group-twice-preview
  cda-mini-birthsex-filter
)

for journey in "${journeys[@]}"; do
  echo "=== CDA mini journey: $journey"
  node "$ROOT/scripts/run-builder-journey.mjs" \
    --manifest "$MANIFEST" \
    --journey "$journey" \
    --trace "$ARTIFACT_DIR/$journey-trace.json" \
    --report "$ARTIFACT_DIR/$journey-report.json"
done

echo "CDA_MINI_JOURNEYS_OK ${#journeys[@]} journeys; artifacts: $ARTIFACT_DIR"
