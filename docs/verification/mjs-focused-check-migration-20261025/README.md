# Focused-check integration history

The Group Pivot Join and DiagnosticReport Append oracle checks were attached by a one-time migration. They are now declared directly in `scripts/verify-ui/registry.mjs`. The migration tool, family-discovery rules, semantic catalog, and captured migration reports have been retired. The executable oracle tests remain.

`focused-check-batch.json` remains a reusable checks-only batch for those two registered cases. Run it with `node scripts/run-native-verification-bracket.mjs --batch docs/verification/mjs-focused-check-migration-20261025/focused-check-batch.json --checks-only`. A checks-only pass does not establish a browser lifecycle.

The original reports, logs, hashes, and migration notes remain available in Git at `3ffcdfa58`. To inspect them without restoring generated artifacts to the checkout:

```sh
mkdir -p /tmp/loom-historical-evidence
git archive 3ffcdfa58 docs/verification/mjs-focused-check-migration-20261025 | tar -x -C /tmp/loom-historical-evidence
```
