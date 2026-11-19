# Ingestion round 4: implementation plan

**Base:** `perf/ingest-memory` at `b7a35f59d`.

**Goal:** cut wall time and memory on large loads. The primary workload is the full CDA-FHIR load:

- 1.93 GB, 2,055,978 rows;
- `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META`, the 11 NDJSON files (skip `CONFIG`).

META_SMALL's CPU floor stays as the quick check.

## Where the time goes now (measured on the CDA run at `b7a35f59d`)

- **Real load: 165 s.** The no-write load takes 63.8 s, so most of the wall time is writes.
- **The 8 writers are busy for almost the whole load.**
  - Edges: 715 writer-seconds of requests, 2.61 GB sent.
  - Vertices: 434 writer-seconds, 2.65 GB sent.
- **Arango writes far more to disk than it receives.** A single load writes about 50 GB to disk (Docker block I/O delta; for example 252 → 304 GB). The final data is 3.37 GB of live SST files, and the loader sends 5.3 GB. The CDA run recorded 21 RocksDB write stalls; META_SMALL had none, which is why Track C never tested RocksDB settings.
- **ResearchStudy.ndjson finishes last in every run, taking 26 s.**
  - It has 429 rows, some up to 4.3 MB, each holding up to 26,408 extensions.
  - Loaded alone with no writes, it takes 19.8 s.
  - 81% of that CPU is in `extensionURLAncestry` (`internal/catalog/semantic_schema.go:715`). For every extension scope it loops over every extension URL in the row, so the cost grows with the square of the number of extensions.
- **Loader allocations: 136 GB per no-write CDA load.**
  - `walkMemberScope` rebuilds the `declared` and `required` maps on every call (`internal/fhir/semantic`, lines 261 and 282). That's about 43 GB.
- **Write time is mostly a per-document cost, not a per-byte cost.** This is inferred from a two-point fit of `request_seconds` against documents and bytes:
  - about 126 µs per document plus about 67 s per GB;
  - per-document cost accounts for about 800 of the 1,150 writer-seconds.

  Each document triggers one RocksDB write for itself plus one for every index entry: 4 for a vertex, 5 for an edge. **Fewer index entries per document** is therefore a bigger lever than fewer bytes. Shorter keys still help, but expect less from them than the bytes alone suggest.
- **Arango memory peaked at 7.4 GB.** RocksDB's block cache is sized by default as a share of the VM's RAM (19.5 GB here), so most of that is cache, not need.

## Tracks

Tracks 1 and 2 share no files and run in parallel. Track 3 changes only compose files.

### Track 1: Loader CPU and memory

Owner files:

- `internal/catalog/semantic_schema.go`
- `internal/fhir/semantic/*`
- `internal/fhir/schema/fields.go`
- `internal/catalog/helpers.go`
- `internal/catalog/write_profiler.go`
- the line handoff in `internal/ingest/generation_workers.go`

1. **Find extension ancestors by depth, not by scanning.** In `extensionURLAncestry`, walk the prefixes of `ownerPath` at each `.` boundary, plus `ownerPath` itself, and look each one up in `urls`. Keep the non-empty URLs, shortest owner first. The result is the same, but the cost depends on path depth instead of extension count.
   - Test: the old and new versions return identical results on generated paths, including nested extensions and owners that share a prefix without a `.` boundary.
   - Measure: ResearchStudy alone, plus the CDA tail.
2. **Cache the scope maps.** Build `declared` and `required` once per element definition, cache them, and have callers only read them. Any per-row additions go into a separate small scratch map.
3. **Build the schema once per resource type.** `cachedResourceFields` (`internal/fhir/schema/fields.go:37`) checks the cache, misses, and flattens before it stores. Every worker hits that at the same moment through `ensureStat` → `DefaultPivotSpec` → `LookupField`. On META_SMALL that costs 4.0 GB of allocations and 2.1 s of CPU.
   - Fix: a `sync.Once` or singleflight per type, or build every type eagerly.
   - Also cache empty results.
4. **Hand lines to workers in batches.** The per-line `select`, channel receive and worker-budget permit cost 5.7 s of CPU (`selectgo`) on META_SMALL.
   - Send groups of about 256 lines and take one permit per group.
   - Keep row numbers, fail-fast behavior and progress events unchanged.
5. **Hash the shape fingerprint.** Replace the string built by `shapeFingerprintForValue` (1.2 s CPU, 1.6 GB on META_SMALL) with a 64-bit hash computed without allocating:
   - combine object keys in an order-independent way;
   - combine array items as a set of distinct child hashes.

   Keep the string on each cached plan, and add a debug test that checks for collisions.
6. **Shrink the write queue.** `WriteQueueSize` is 16 batches of 5,000 documents per file, with 2 files at once, and each worker also holds its own batches. The writers are already saturated, so those queued documents only hold memory; they don't add throughput.
   - Try 2 and 4, measuring loader peak RSS and CDA wall time.
   - Change the default only if wall time stays within the baseline spread.

### Track 2: Smaller documents and fewer index entries

Owner files:

- `internal/ingest/generation_identity.go`
- `row_builder.go`, `documents.go`, `generated_load.go`
- `source_load_copy.go`, `source_load_upload.go`
- `internal/ingest/backend.go`, `internal/ingest/index_usage_test.go`, `docs/ARANGO_INDEX_USAGE.csv`
- `internal/catalog/arango/relationships.go`
- the id-lookup renderers: `internal/dataframe/compiler/render/aql/group_rows.go`, plus whatever renders `selection-reference-validation`
- any reader the audit turns up

Order: 7 → 8 → 9 → 10 → 11 → 12.

- Item 7 needs no code, so start it as soon as the baseline exists.
- Item 10 depends on the key format from item 8.
- Items 10 and 11 are skipped if item 7 shows their index costs less than the run-to-run spread. Log the skip with its number.
- Track 2 has one owner, so these items run one after another.

7. **Measure each index's write cost first.** Before changing any reader, run the CDA load into benchmark databases that leave out one index at a time:
   - the edge persistent index;
   - the vertex `{project, dataset_generation, id}` index;
   - the vertex `{project, dataset_generation, auth_resource_path, _key}` index.

   Record writer-seconds, wall time and disk bytes for each. That sizes the gain from items 10 and 11 before anyone touches the readers.
8. **Shorter keys.** Generation keys become a new prefix plus the first 32 hex characters of the SHA-256 (128 bits): 34 characters instead of 66.
   - Hex rather than base64, so that AQL can compute the same key with `SUBSTRING(SHA256(...), 0, 32)`. Item 10 depends on that.
   - The key's inputs and their sanitization must be expressible in AQL. If the current `\0` separators or `SanitizeKey` can't be reproduced exactly in AQL, change the new format's encoding until they can.
   - Add a differential test: the Go key and the AQL key are equal against a live Arango.
   - The new prefix lets the old and new formats be told apart.
   - Encode into a reused buffer. That also removes the 1.3 GB of `hex.EncodeToString` allocations.
   - Audit every caller that recomputes a key for an *existing* generation. Each must use that generation's own format; record the format in the generation manifest if any such caller exists.
   - Source-load copy writes the new format into the new generation.
9. **Drop `logical_from` and `logical_to` from edges.**
   - Their only reader is `source_load_copy.go:229`. It already falls back to the endpoint vertices' `logical_key`, which the copy query always fetches (lines 123–128).
   - Make that fallback the main path, and add a test that copies a generation written without the two fields.
   - Keep `logical_key` on both vertices and edges.
10. **Replace the vertex id index with primary-key lookups.** Today, looking up a resource by its FHIR id within a generation uses the `{project, dataset_generation, id}` index. With item 8, the key can be computed from (project, generation, collection, id), so these lookups can fetch the document by key from the primary index instead, and the index can go.
    - Readers to switch (from `docs/ARANGO_INDEX_USAGE.csv`):
      - `resource-id-generation`;
      - `group-row-source-lookup`;
      - `selection-reference-validation`.
    - Compute the key in Go and pass it as a bind variable where the ids are known up front, and in AQL where they come from other documents.
    - Check `doc.id == id` after every fetch, so a sanitized-key collision can never return the wrong resource.
    - Vertices go from 4 RocksDB writes to 3.
    - Do this only if item 7 shows the index costs more than the run-to-run spread.
    - It applies only to generations written with the new key format. Old generations keep their index and use their current query path.
11. **Replace the edge persistent index.** It's the only edge index we add on top of Arango's own, and it's large: about 6 fields plus a stored value per edge. Its readers (from `docs/ARANGO_INDEX_USAGE.csv`), with replacements:
    - `relationship-edge-count`: read the counts from `fhir_relationship_catalog`, which the loader already fills from its in-memory counts.
    - `relationship-catalog-rebuild` and `mixed-auth-edge-scan`: these are rare. Walk the generation's vertices through their index, then each vertex's outgoing edges through the edge index.
    - `source-file-cleanup` (`source_load_upload.go:301`): the owning file is `from_type`'s collection. Iterate that file's vertices in the generation, and delete their outgoing edges through the edge index.
      - Today the cleanup deletes vertices **before** edges. With this change, edges must be deleted first, because a vertex is the only way to reach its edges.
      - A crash between the two steps must not leave edges that nothing can reach. Add a test that interrupts cleanup after the edge step and after the vertex step, and checks a retry leaves no orphaned edges.
      - If that can't be guaranteed, keep the index; don't add a fallback full scan.
    - `source-load-edge-auth-scope`: rewrite the same way, starting from the endpoint vertices.

    Time each rewritten query on CDA before and after. Do this only if item 7 shows the index is material, and record which rare operations got slower and by how much. Edges go from 5 RocksDB writes to 4.
12. **Smaller vertices.** For each candidate field, list its readers. Drop it only where nothing reads it, or where a reader can switch to an equivalent field cheaply.
   - Candidates:
     - top-level `project_id` (duplicates `project`);
     - top-level `resourceType` (the same for every document in a collection);
     - `project_id` injected into the payload;
     - top-level `id` (duplicates `payload.id`).
   - `project_id` is a column name across the dataframe, ClickHouse and publication code, so check publication output before dropping it.
   - Record each decision with its readers in the commit message.

### Track 3: Arango write amplification

Owner files: `tests/deploy/compose*.yaml` (only the arangod flags).

13. Run one CDA load per setting, each on a fresh container. For each, record:
   - wall time;
   - disk bytes written (block I/O delta);
   - write stalls;
   - Arango peak memory.

   Settings to try:
   - larger memtables: `--rocksdb.write-buffer-size`, `--rocksdb.max-write-buffer-number`, `--rocksdb.total-write-buffer-size`;
   - `--rocksdb.min-write-buffer-number-to-merge`;
   - level sizing, starting from `--rocksdb.dynamic-level-bytes`, plus the level-0 triggers;
   - compression settings.

   Rerun the best two settings 3× each, after Track 2 has landed, because smaller documents change the write amplification. Keep a setting only if it beats the run-to-run spread, and report its memory cost. Fresh installs benefit straight away; existing deployments need the compose change.
14. **Cap Arango's memory.** Set `--rocksdb.block-cache-size` (try 512 MB, 1 GB and 2 GB) and `--rocksdb.total-write-buffer-size` in the compose files, so a laptop install doesn't hand a large share of its RAM to the cache.
    - Measure CDA wall time and Arango peak memory, plus the warm query timings from round 2's query set.
    - Pick the smallest cap that keeps load wall time and query p95 within 10%.

## Measurement

- **Reuse the round-3 scripts** from `/private/tmp/loom-ingest-memory/outputs/ingest-perf/` (`cda/runner.sh`, `cda/real-profile.sh`, the CPU-floor test binary build, `capture.py`, `compare.py`).
- **Add to each run:**
  - Arango disk bytes written per load;
  - RocksDB stall count;
  - the per-file duration of the last file.
- **Baseline at `b7a35f59d`:**
  - CDA real load, 3 runs, each on a fresh container. Round 3 reused one container, which let memory and cache state build up.
  - CDA no-write load, 3 runs.
  - META_SMALL CPU floor, 3 runs.
  - CPU and allocation profiles.
  - Capture the stored output for the equivalence checks.
- **Per item:** measure on the CPU floor (Track 1) or the CDA real load (Track 2). Keep the change only if wall time or allocated bytes improve by more than the baseline spread; otherwise revert it and log why. Item 1 counts as kept if ResearchStudy alone improves.
- **Log every attempt** in `outputs/ingest-perf/decision.tsv`.

## Commits and checks

- One commit per kept item, or one per tightly related pair (3 + 2 is fine).
- Before each commit:
  - the item's own tests;
  - `make test-go`, `make lint-go-comments`, `make test-integration`;
  - `pstack:interrogate` on the exact diff.
- **Integrate:** Track 1, then Track 2, then Track 3's compose change.

## Final rerun

Run the baseline and final binaries interleaved, 3 rounds, on the CDA real load (fresh container per run), the CDA no-write load, and the META_SMALL CPU floor. Report medians and spread for:

- wall time;
- loader CPU-seconds;
- loader peak RSS;
- allocated bytes;
- garbage-collection cycles;
- request bytes per class;
- Arango disk bytes written;
- stalls;
- Arango peak memory;
- the last file's duration.

**Equivalence against the baseline capture:**

- vertices and edges identical after normalization, accounting for the new key format and any dropped fields;
- the same graph, with the endpoints mapped through the logical keys;
- inventory entries identical;
- field-catalog differences limited to the capped samples already seen in round 3.

**Query performance and results against the baseline** (required, because items 10, 11 and 14 change indexes and cache):

- Rerun round 2's query set on the CDA database: the dataframe compiler renders from the conformance and recipe fixtures, `graph_service.go`, the explorer, catalog, relationship and evidence queries, and source-load copy and cleanup.
- Use warm p50/p95 timings, same results row for row, and p95 within 10% of the baseline.
- Also time the operations that item 11 moved off the index:
  - relationship rebuild and audit;
  - single-file re-upload cleanup;
  - source-load copy.

  Report each one's before and after.
- The index guard test (`index_usage_test.go`) passes against the final index set.

**Measure only (no changes this round), so the next round has numbers:**

- **First query after a fresh load:** time the first Builder table and pivot preview on CDA right after loading. The first query lazily creates its `loom_pivot_preview_*` covering index over the whole collection; for Observation that's 815k documents, so the new user's first table may wait on an index build.
- **Publication:** time the ClickHouse publication step that `verify-loom` runs, on CDA, end to end.

Then run `verify-loom`, including ClickHouse publication.

## Not doing

- More writers or cores, which is a hardware change.
- Reshaping the data.
- Removing the second JSON parse, which would mean rewriting the profiler.
