# Real NCPI generation

## Sub-features

- Fetch the locked NCPI FHIR Aggregator resources on a cold cache miss.
- Reuse the canonical cache without network access on a warm hit.
- Upload the per-type generation and compare load and direct Arango counts.

## How to get to it (user POV)

Run `make acceptance-real`; the command's first production action is the
generation upload.

## Driving it with HTTP

The helper posts the cached `*.ndjson` files to the generation endpoint and
records the load response in `report.json`. It then counts each locked type in
the run-specific Arango database.

## Gotchas

The checked-in lock is metadata-only. Any ID, version, or canonical SHA-256
drift fails the run and never rewrites the lock.
