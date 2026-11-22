# FHIR metadata follow-up after ingest tuning

Prioritize Arango write experiments on `perf/ingest`. Defer the FHIR metadata redesign until the ingest baseline settles. The redesign would preserve typed edge extraction, so its expected benefit is simpler maintenance. Deriving metadata from the pinned inputs could add startup cost.

## Measure Arango writes first

The B4 valid-subset run took 94.62 seconds. Eight writers accumulated about 647 seconds inside insert calls, including about 319 seconds for `fhir_edge`. Dividing by eight gives a lower bound of about 81 seconds with at least one insert in flight. Loading work can overlap inserts, and the timings include client and server work. Use end-to-end wall time to judge changes.

Coordinate the batch-size sweep with the write-path experiments on this branch. Compare 5,000, 10,000, and 20,000 documents per batch at eight writers. Keep the valid subset and starting database state equivalent. Isolate edge batch size if the loader supports it. Use repeated wall times and verify matching edge counts and IDs. Record request bytes, writer-permit wait, and request duration by collection. If batching fails to improve wall time and writers remain backed up, sweep writer concurrency.

## Revisit schema metadata against the final baseline

Derive field definitions and reference rules from the pinned FHIR R5 files and Loom's supported-resource list in one shared package. Use those rules to generate the typed edge extractors and build the runtime field and traversal indexes once.

Remove the checked-in metadata map after verifying field resolution, edge identities and deduplication, forward and reverse query routes, integer64 normalization, and published schema identity. Measure initialization time, binary size, and steady-state ingestion against the final `perf/ingest` baseline before choosing how to ship the pinned inputs. Keep a generic extractor as a separate decision backed by edge parity and throughput evidence.
