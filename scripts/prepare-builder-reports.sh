#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."
for generator in \
  build-test-obligations \
  propose-builder-route-actions \
  propose-builder-contract-proofs \
  propose-builder-browser-cases \
  propose-builder-mutation-probes \
  run-builder-bug-discovery \
  build-builder-readiness
do
  node "scripts/${generator}.mjs"
done
