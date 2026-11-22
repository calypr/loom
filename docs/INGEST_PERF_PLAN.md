# Ingestion performance plan

Branch: `fhir/own-generation`. Goal: cut generated-FHIR ingestion wall time for a new user without
changing what the published dataset contains.

The work is four implementation items plus a final apples-to-apples rerun of the
baseline benchmark. It runs straight through with no checkpoints between phases.

## Baseline (2026-11-18)

Measured with a probe that runs the real `loadFile` pipeline on `META_SMALL` with a no-op Arango
inserter (CPU side only). Generation namespacing is on (`PERF_GEN=gen-1`).

| Run (Arango insert replaced by a no-op)     | Wall  | rows/s |
|---------------------------------------------|-------|--------|
| Current                                     | 35.9s | 10.3k  |
| GOGC=400                                    | 32.3s | 11.5k  |
| Profiler on, semantic inventory not written | 29.9s | 12.4k  |
| Profiler skipped                            | 6.5s  | 56.7k  |

Baseline identity:

- Source: `cd461c7a1` plus the uncommitted vertex-namespace change in `row_builder.go`,
  `generation_identity.go`, and their tests.
- Toolchain and host: Go 1.27.4 darwin/arm64, 10 cores, 64 GB.
- Dataset digest: `ee1d460a880f10a96d5f10a54d863aabd9aafa56f89a3f2b7507e7653d839d0a`, computed as
  `shasum -a 256 META_SMALL/*.ndjson | shasum -a 256`.

Other baseline facts:

- Volume: 370,048 rows. 5,920 `ImagingStudy` rows fail validation (all of them) and are excluded.
- Stage times (worker-seconds): `field_profile` 212, `decode` 21.9, `field_shape_fingerprint` 6.6,
  `payload_map_decode` 4.1, `edge_generation` 2.7, `vertex_marshal` 2.4, `validate` 1.0.
- Output volume:

  | Collection | Docs | Size |
  |---|---|---|
  | vertices | 370,048 | 666 MB |
  | `fhir_edge` | 756,130 | 458 MB |
  | `fhir_semantic_inventory` | 2,739,454 | 3,160 MB (74% of all bytes written) |

- CPU profile: 157s of samples over 36s of wall time, so about 4.4 of 10 cores are busy.
  Heavy heap-lock contention (`runtime.lock` from `mheap.allocSpan`) indicates allocation churn.
- Inside `observeSchemaSemanticsWithEvidence` (65s cumulative):
  - `semantic.WalkMembers`: 28.5s
  - stdlib `json.Unmarshal`: 17s (per-level `decodeObject` plus `decodeSemanticObject`)
  - stdlib `json.Marshal`: 10s (payload re-encode plus one marshal per contribution)
  - `recordSchemaSemanticObservation`: 13s

## Phase 0 — Benchmark harness and baseline snapshot

The final comparison is only valid if the harness is landed first and baseline numbers come from it.

1. Land `internal/ingest/ingest_perf_test.go` as an opt-in test. It is skipped unless
   `LOOM_INGEST_PERF_META` is set; `LOOM_INGEST_PERF_GEN` optionally sets the generation.
   - It runs `loadFile` per file with a no-op counting inserter.
   - It prints, per file: rows, edges, wall time, rows/s, and error counts.
   - It prints totals: wall time, rows/s, and worker-seconds per stage.
   - It prints docs and bytes per collection class: vertices, `fhir_edge`, `fhir_semantic_inventory`.
   - It emits one machine-readable JSON line (`perf_result`) so runs can be diffed.
   - It must not contain the `PERF_SKIP_PROFILE` / `PERF_NO_INVENTORY` switches. Those exist only in
     the throwaway worktree `/private/tmp/loom-ingest-perf` and are used for ablation rows.
2. Add a capture mode (`LOOM_INGEST_PERF_CAPTURE=<dir>`). It writes every inserted document to
   per-collection files, which Phase 5 needs for output-equivalence checks. Normalize the output:
   - Decode each document and re-encode it with sorted keys.
   - Sort vertices and edges by `_key`.
   - Aggregate inventory by the materializer key (see Phase 2).
3. Add `make ingest-perf` to run the probe, with `GOGC` unset, `-count=1`, and a `-cpuprofile`
   written under `outputs/ingest-perf/<label>/`.
4. Commit together with the vertex-namespace change (if it isn't committed already). Tag that
   commit `B0` in `outputs/ingest-perf/README` (untracked) with its SHA.
5. Record baseline B0: 3 timing runs plus 1 capture run. Keep `cpu.pprof`, `perf_result` JSON,
   and captures in `outputs/ingest-perf/B0/`.
6. Real-Arango baseline: run one full generation load of `META_SMALL` with the `verify-loom` skill.
   Record total wall time and the times reported in `go_load_complete`, `go_resource_inventory_complete`
   and the activation events. This is the only place inventory write volume shows up as time.
   Serialize it with any other runtime use.

## Phase 1 — Stop re-parsing each row in the semantic walk

Owner files:

- `internal/catalog/semantic_schema.go`, `semantic.go`, `write_profiler.go`
- `internal/fhir/semantic/member_walk.go`, `walk.go`

Expected effect: removes most of the ~35s of stdlib JSON work inside `field_profile` and much of the
allocation churn.

### 1a. Walk the already-decoded payload

- Add `semantic.WalkMemberValues(index, root, payload map[string]any, visit)`. It walks the decoded
  tree that `GeneratedRowBuilder` already produced. `MemberFact` gains `Value any`, set to the
  member's decoded value. Today each `walkObjectMembers` level calls `decodeObject` into
  `map[string]json.RawMessage`, which is O(depth × size) re-decoding.
- Keep the byte-based `WalkMembers` for current callers that need `RawJSON` (coverage census, retained
  backfill). Implement both walks over one internal walker parameterized by node access, so their
  semantics cannot drift. Shared behavior includes diagnostics, limits, resource boundaries,
  resolution of `resourceType` for contained resources, and choice groups.
- In `observeSchemaSemanticsWithEvidence`:
  - remove `json.Marshal(payload)`;
  - call the value walker;
  - replace `decodeSemanticObject(fact.RawJSON)` with `fact.Value.(map[string]any)`.
- Fact order must stay identical, because contribution ordinals (and today's contribution keys)
  depend on it. The byte walker iterates in key order from `encoding/json` map decoding plus explicit
  sorting. The value walker must sort map keys the same way.
- The coverage path (`observeSemanticObservationsWithCoverage` with `coverageSink != nil`) still needs
  bytes for `CensusSemanticCoverage`. Pass the row's existing payload bytes through
  (`VertexDocument.Payload` is exactly the sonic encoding of `payload`) instead of re-marshaling.
  This is used only by retained and backfill callers, not the generation hot path.

Tests:

- **Differential test** in `internal/catalog`. For every row of a fixture set (the existing
  `META`/fixture NDJSON and the semantic test payloads), the byte walk and the value walk must emit the
  same `[]MemberFact` minus `RawJSON`/`Value`, with `RawJSON` decoding equal to `Value`. The full
  observation sequence (observation, examples, evidence) must also be equal.
- Add property cases: contained resources, choice arms, extensions with URL-dependent dispatch,
  nulls, empty arrays, and repeated codings.

### 1b. Shared fingerprint and shape-keyed semantic plan

Do this after 1a lands and is measured.

- `shapeFingerprintForValue` (6.6 worker-s) builds nested strings for every row. Compute it once per
  row and reuse it for both the field plan and the semantic plan. Switch it to an incremental 64-bit
  hash and keep the string only on a cache miss.
- Cache a semantic plan keyed by (fingerprint, resource type, plus values that change dispatch):
  - `resourceType` of contained or nested resources;
  - `Extension.url` values that select extension associations.

  The plan stores the ordered scope and fact skeleton; per-row work reads values through the plan's
  accessors. Bound the cache with the existing `MaxShapePlans`/`MaxRetainedBytes` budget.
- Gate: keep 1b only if it beats 1a on the Phase 0 harness by a measured margin and passes the same
  differential test (plan path vs uncached path).

## Phase 2 — Pre-aggregate semantic inventory contributions

Owner files:

- `internal/catalog/semantic_inventory.go`
- `internal/ingest/generation_workers.go` (inventory sink only)
- `internal/ingest/source_load_copy.go` (verify only)
- `internal/catalog/arango/semantic_inventory.go` (verify only)

Expected effect: 2.74M docs / 3.16 GB down to roughly one doc per distinct
(auth path, resource type, binding, concept) per file. Also removes one `json.Marshal` per
contribution.

Why it is safe: every reader of `fhir_semantic_inventory` already aggregates.

- `materializeSemanticInventoryEntriesAQL` groups by
  `(auth_resource_path, resource_type, binding_id, concept_id)` and computes `SUM(population)`,
  `MIN` of each scalar field, and `MAX` of `units` and `examples_truncated`.
- The resolve-family and selection queries (`semantic_inventory.go:800–1100`) use
  `SUM(population)`, `SUM(1)` only in a `> 0` filter, `MIN`, and `MAX`.
- No AQL reader filters on `source_id` or `ordinal`.
- Copy (`copySemanticInventoryDocument`) rekeys from `source_id` and `ordinal` and only requires
  them to be non-empty and non-negative.
- Cleanup (`clearCandidateSourceFile`) filters by `dataset_generation` and `resource_type`.

Implementation:

1. Add `catalog.SemanticInventoryAggregator`. It is keyed by the materializer's `COLLECT` key and
   combines contributions with exactly the AQL semantics:
   - `population` is summed.
   - Every field the materializer reads with `MIN` is reduced field by field with ArangoDB
     ordering. Strings use binary order; `null` sorts lowest, and `MIN` skips nulls the way AQL does.
     Arrays (`fallback_selectors`, `extension_url_path`) are compared element-wise.
   - `observed_units` keeps both its min and max.
   - `examples_truncated` is OR-ed.
   - `example` takes the `MIN`.

   Write the reducer table next to the AQL constant and add a test asserting it covers every
   `MIN(...)`/`MAX(...)` in `materializeSemanticInventoryEntriesAQL` and the resolve queries. Parse the
   AQL text so a new aggregate field fails the test.
2. Workers feed contributions into a per-worker aggregator instead of
   `json.Marshal` → `semanticInventoryBatch`.
   - At file end, merge worker aggregators in worker-independent order: sort by key.
   - Emit documents with `source_id = <semanticSourceFile>` (e.g. `META/Observation.ndjson`) and
     `ordinal = index in sorted order`.
   - Key them with the existing `semanticInventoryContributionKey` inputs, so `_key` stays
     deterministic for retries and copy.
   - Encode with sonic.
   - If an aggregator exceeds a size cap (e.g. 50k keys), flush a deterministic chunk with a chunk
     ordinal. Duplicate keys across chunks are still correct, because all reducers are associative.
3. `fileWriteTask{replace: true}` semantics stay the same.
4. Retained backfill (`SemanticInventoryEmitter`) is out of scope and keeps per-source rows. The
   materializer must accept both shapes, which it does because it only aggregates.
5. Decide during implementation whether this change needs a new `inventory_schema` version. If no
   reader distinguishes row-level from file-level docs (current evidence says none does), don't bump
   it, and record that decision in the commit.

Tests:

- **Unit:** the aggregator on randomized contribution sets equals a Go reference implementation of
  the materializer's `COLLECT`/`AGGREGATE`, including null, empty-string, and array cases.
- **Integration (Arango, `make test-integration`):**
  1. Load a fixture generation twice, once with row-level contributions (old path behind a
     test-only switch) and once aggregated.
  2. Run `EnsureSemanticInventoryEntries`, `PageSemanticInventory` and
     `ResolveSemanticInventorySelections` on both.
  3. Assert identical entries and results.
- Source-load copy of an aggregated generation still produces unique keys and identical entries.

## Phase 3 — Load files concurrently

Owner files: `internal/ingest/generation_orchestration.go`, `internal/ingest/generation_workers.go`
(worker and writer pool plumbing). Do this after Phase 2, because both edit `generation_workers.go`.

Expected effect: removes the per-file drain barrier, where small files leave cores idle. The size of
the gain depends on Phases 1–2 and must be measured.

1. Replace the sequential `for _, file := range files` with bounded concurrency
   (`opts.FileConcurrency`, default 2).
   - Order files by descending size, so the longest jobs start first and fewer tail files are left
     running alone.
   - Use `errgroup.WithContext` for cancellation; with fail-fast, one file failing cancels its siblings.
2. Share the CPU and Arango budgets across files instead of multiplying them:
   - a global row-worker semaphore of size `WorkerCount`;
   - a global writer semaphore of size `WriterCount`, so Arango never sees more than `WriterCount`
     concurrent imports;
   - a shared `ShapePlanCache` per resource type.
3. Keep ordered side effects ordered:
   - `AdvanceSemanticInventoryBuild` checkpoints advance only over the contiguous prefix of completed
     files in the original file order. Resume semantics are unchanged.
   - `rowErrorCandidates` is indexed by original file position, so
     `selectGenerationRowErrorSamples` output is unchanged.
   - Catalog merges and `MergeRelationshipCounts` run under a mutex, or after `Wait()`; both are
     commutative.
   - `go_load_progress` uses shared atomic insert counters instead of the
     `priorVertices`/`priorEdges` snapshot.
   - `go_load_file_start`/`complete` are still emitted per file. Their order across files may
     interleave; check the UI and progress consumers for any ordering assumptions.
4. `UploadFile` (single-file source load) and `resource_load.go` keep sequential behavior.

Tests:

- Generation load summary, catalogs, relationship counts, row-error samples, and checkpoints equal
  the sequential result when run with `FileConcurrency=1` and `=3`.
- Cancellation mid-load leaves the failed-generation cleanup path working. Existing
  `generation_failed_cleanup_e2e_test.go` and the checkpoint tests must pass under concurrency, run
  with `-race`.

## Phase 4 — Build edges once, already namespaced

Owner files:

- `cmd/generate/extraction.go`, `fhir/model/extract.go` (regenerated)
- `internal/ingest/row_builder.go`, `generation_identity.go`, `source_load.go`
  (`sourceFileOwnerRowBuilder`)
- `internal/catalog/relationships.go`

This is independent of Phases 1–3 and can run in a separate worktree in parallel with Phase 1.

Today each edge is built as raw JSON and then decoded and re-encoded up to three more times, plus
decoded once more for counting:

1. `edgeWithAuthResourcePath` (sonic, via a map);
2. `namespaceEdgeDocument` (stdlib, via `map[string]json.RawMessage`);
3. `sourceFileOwnerRowBuilder` (stdlib) for source loads;
4. `RelationshipCountsFromRawEdges` decodes once more, per batch.

Implementation:

1. Generator: emit `ExtractEdgeDocuments(project string) ([]EdgeDocument, error)`, which appends the
   existing `EdgeDocument` structs. Keep
   `ExtractEdges` as a thin generated wrapper that marshals them, for the
   `related_medication_fixture_test.go` callers and `cmd/generate/extraction.go:239`. Regenerate with
   `make generate-fhir`.
2. Ingest: add an ingest-local `edgeRow` struct that embeds `fhir.EdgeDocument` plus
   `auth_resource_path`, `dataset_generation`, `logical_key`, `logical_from`, `logical_to`, and
   `source_file`, with `omitempty` where the old documents omitted the field.
   - `GeneratedRowBuilder.build` fills those fields directly.
   - Apply `generationDocumentID`/`generationEdgeKey` to the struct's strings.
   - Marshal once with sonic.
   - This extends the vertex change already in progress.
3. `rowBuildResult` carries `[]catalog.RelationshipKey`, or a per-row count delta, computed from the
   structs. `flushEdgeBatch` then merges keys instead of calling `RelationshipCountsFromRawEdges`.
   Keep that function for any other callers.
4. `sourceFileOwnerRowBuilder` becomes a builder option (`sourceFile string`) instead of a
   decode/re-encode wrapper.
5. Keep the validation `namespaceEdgeDocument` performs:
   - non-empty `_key`, `_from` and `_to`;
   - `collection/key` shape;
   - project match.

   Validate the struct before marshaling. Failures still map to `rowErrorGeneration` or
   `rowErrorEdge` as today.

Tests:

- **Differential:** for every row in the fixtures and `META_SMALL`, the new edges decoded to
  `map[string]any` deep-equal the old path's edges. Cover all combinations of with/without auth path,
  generation, and source file.
- Relationship counts from the struct path equal `RelationshipCountsFromRawEdges` on the old output.
- Existing tests to keep passing:
  - `row_builder_test.go`
  - `generation_identity_test.go`
  - `keycollide_generated_e2e_test.go`
  - `related_medication_fixture_test.go`
  - `make test-go`

## Ownership and parallelism

- Phase 0 runs first; it is short and sets the measuring stick.
- Then, in separate worktrees:
  - **Phase 1** (catalog / semantic walk) and **Phase 4** (generator / edges) touch disjoint files
    and run in parallel.
  - **Phase 2 → Phase 3** run in sequence in one worktree, since both edit `generation_workers.go`.
    Phase 2 can start alongside 1 and 4. Its only overlap with Phase 1 is the sink signature in
    `semantic.go`, which Phase 1 leaves unchanged.
- The parent integrates in order 1, 4, 2, 3, rebasing each onto the integrated tip, and runs the
  Phase 0 harness after each integration. These are the B1–B4 points in Phase 5.
- Each item gets one commit (implementation, tests, harness numbers in the message) after
  `pstack:interrogate` review of its snapshot, per `AGENTS.md`.

## Verification per item

- Closest checks: `go test ./internal/catalog/... ./internal/fhir/... ./internal/ingest/...`,
  `make test-go`, and `make lint-go-comments`.
- Run `make test-integration` for Phases 2 and 3.
- Ingestion changes also go through `.codex/skills/verify-loom/SKILL.md` before final acceptance
  (one real generation load plus publish of `META_SMALL`).
- Harness capture equivalence against B0:
  - vertices: byte-identical after normalization;
  - edges: identical after normalization;
  - materialized inventory entries: identical.

  Phase 2 changes the raw inventory docs by design, so compare its *entries*, not raw docs.

## Phase 5 — Apples-to-apples rerun

Goal: show what improved, and by how much per item, on the same terms as the baseline table above.

Fixed conditions (record them in the results):

- Same host, idle; no other `go test` or Loom runtime work during the runs. Check with `top` or
  `uptime` before starting.
- Same dataset: `META_SMALL` with digest `ee1d460a…d839d0a`.
- Same toolchain: Go 1.27.4.
- Same harness: the Phase 0 harness at its B0 version. If the harness changes later, rerun B0 with
  the new harness.
- Same options: default `LoadOptions` (`WorkerCount` = `WriterCount` = 8, `BatchSize` 5000),
  `LOOM_INGEST_PERF_GEN=gen-1`, `GOGC` unset, no-op inserter.

Procedure:

1. Build one test binary per snapshot (`go test -c`):
   - **B0**: baseline
   - **B1**: + Phase 1
   - **B2**: + Phase 4
   - **B3**: + Phase 2
   - **B4**: + Phase 3, the final tip
2. Run the binaries interleaved round-robin: B0, B1, B2, B3, B4, then B0, B1, and so on, 3 rounds,
   so drift and thermal effects spread evenly. Report the median wall time and rows/s. Discard any
   round whose B0 differs from the B0 median by more than 10%, and rerun it.
3. Also run each snapshot once with `-cpuprofile`, to report CPU utilization
   (samples ÷ wall ÷ cores) and the top stages.
4. Recompute the ablation rows on B4 in the throwaway worktree pattern. This shows how much
   headroom remains:
   - with the profiler skipped;
   - with `GOGC=400`.
5. Run the capture-equivalence check for B4 against B0.
6. Run the same real-Arango load as Phase 0, step 6, on B4, and compare against the B0 real-load
   numbers. This is where Phase 2's reduced write volume shows up.

Report as a table with one row per snapshot. Columns:

- Wall (median of 3)
- rows/s
- Δ vs B0
- Δ vs previous snapshot (attributes the gain to the item)
- `field_profile` worker-s
- `decode` worker-s
- CPU utilization
- `fhir_semantic_inventory` docs / MB
- `fhir_edge` MB

Add rows for real-Arango end-to-end at B0 and B4. State what remains unverified, for example
datasets other than `META_SMALL`, or ClickHouse publication if `verify-loom` didn't cover it.

## Out of scope (noted for later)

- Collapsing the typed decode, map decode and map re-encode for each row (`decode` 21.9 worker-s,
  `payload_map_decode` 4.1).
- Retained/backfill inventory emitter aggregation.
- The `ImagingStudy` validation failures in `META_SMALL`: 5,920 of 5,920 rows. Triage separately;
  this is a data or validation correctness question, not performance.
