# Batch verification history

This closeout originally recorded the table-reorder and DATE focused checks plus a CDA browser run on source `e96ca4b28e7c748104a00447b9d2316537f175ef`. The executable cases and their assertions remain registered. Captured report/log payloads are historical output, not current verification inputs.

`first-batch.json` remains a reusable example selecting `builder-authoring:table-reorder` and `basic-temporal-filter:date`. New runs write evidence to an explicitly selected local artifact directory.

The original reports, logs, hashes, and migration notes remain available in Git at `3ffcdfa58`. To inspect them without restoring generated artifacts to the checkout:

```sh
mkdir -p /tmp/loom-historical-evidence
git archive 3ffcdfa58 docs/verification/mjs-batch-journey-integration-20261025 | tar -x -C /tmp/loom-historical-evidence
```
