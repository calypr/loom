# Ingestion write-path implementation plan

Base: `perf/ingest` at PR #216 head (`078924fc3`). Goal: cut the real Arango load time
(94.6 s on the valid META_SMALL subset) by writing less, not by tuning harder.

The plan is optimized for implementation speed. Three tracks run in parallel and share no files.
Measurement brackets them: a baseline before and an apples-to-apples rerun after. Deferred index
builds, key shortening, CPU-floor work, and the retained-backfill path are deliberately left out;
see "Not doing now".

## Why these targets

- **The load is write-bound.**
  - The no-op inserter run takes 25.5 s; the real run takes 94.6 s.
  - Workers spent 234 (edges) + 141 (vertices) worker-seconds waiting on the write queue.
  - Insert time: edges 319, vertices 175, semantic inventory 154 worker-seconds.
- **Inventory writes 559k documents that nothing reads.**
  - The loader writes 559k raw inventory contributions; AQL then collapses them into 941 entries.
  - After a build completes, every reader uses `fhir_semantic_inventory_entries`
    (`internal/catalog/arango/semantic_inventory.go:428`, `:568`).
- **Each document updates about 11 indexes** (`internal/ingest/backend.go:18`). The dataframe
  access patterns can be served by 1–2 merged indexes per collection; the analysis is in Track B.

## Track 0: Measurement (before the tracks start; small)

Owner: parent. Files:

- `internal/store/arango/client.go`
- `internal/ingest/generation_workers.go` (stats emission only)
- `internal/ingest/generation_orchestration.go` (stage timing only)
- untracked scripts under `outputs/ingest-perf/`

1. **Write stats.** Record per collection class: request count, request bytes, writer-permit wait,
   and request duration (sum and p95). Emit them as `write_stats` in `go_load_complete`.
2. **Finalization timing.** Emit `seconds` events for the catalog writes,
   `RebuildResourceInventory`, inventory completion, manifest transition, and activation.
3. **Runner script.**
   - Starts one dedicated Arango container with the default compose image and flags, after
     stopping the other dev stacks.
   - Creates a fresh database per run.
   - Samples `docker stats` and diffs `/_admin/metrics/v2` around each run.
   - Uses the valid subset (`outputs/ingest-perf/valid-meta`) with 8 workers and 8 writers.
   - Extends the existing `run-arango.sh` rather than replacing it.
4. **Baseline C0.**
   - 3 runs of PR #216 head.
   - Save C0's materialized inventory entries. Track A's equality test compares against them.
   - Save the explain plans and latencies of the query set. Track B compares against them.

Commit the instrumentation (1–2) on its own. It's small, and both tracks need it.

## Track A: Write final inventory entries directly

Owner files:

- `internal/catalog/semantic_inventory.go`
- `internal/ingest/semantic_inventory_runs.go`
- `generation_workers.go` (inventory sink only)
- `generation_orchestration.go` (inventory finalization only)
- `internal/catalog/arango/semantic_inventory.go`
- the inventory parts of `source_load_copy.go` and `source_load_upload.go`

1. Turn the aggregator into an entry builder keyed by
   `(auth_resource_path, resource_type, binding_id, concept_id)`. Compute each field exactly as
   `materializeSemanticInventoryEntriesAQL` does today:
   - SUM for population;
   - MIN or MAX for each scalar field;
   - `examples_truncated` = (any truncated) OR (population > 1);
   - `search_text`;
   - the `observation` shape;
   - `_key` = SHA-256 of the same `|`-joined fields.

   Keep the existing bounded spill runs, now keyed by the entry key.
2. Merge worker partials per resource type at the end of the load, and write the entries straight
   into `fhir_semantic_inventory_entries`. Mark the build complete at the current
   `SemanticInventoryEntryIndexVersion`. Generation loads no longer write raw contributions or run
   the materialize AQL.
3. **Single-file upload** clears and rewrites that resource type's entries. **Source-load copy**
   copies entries, re-keyed. The retained backfill path is unchanged.
4. **Example values.** `example` and any free-text MIN fields use plain byte ordering. Arango's
   text ordering may pick a different example for a few entries; the picked value is still a real
   member of the group. This is the default unless the operator asks for exact parity. In that case,
   use `golang.org/x/text/collate` with the server's language, plus a differential test against a
   live Arango.
5. **Tests.**
   - Integration: entries equal C0's entries field for field, except the example-type fields, where
     the test reports how many entries differ and checks each value belongs to its group.
   - Inventory paging, selection resolution, upload replacement, and copy return the same results
     as on C0.
   - `search_text` must match Arango's `LOWER` on the captured values.

## Track B: Merged index design

Owner files:

- `internal/ingest/backend.go`
- `internal/dataframe/compiler/category_scan.go` and `preview_covering_index.go` (preview index
  fields)
- any reader query that needs a rewrite
- `docs/ARANGO_INDEX_USAGE.csv`
- a guard test

**Access patterns** found in the query code:

- Resource collections:
  - generation scan: `project`, `dataset_generation`, optional `auth_resource_path IN`;
  - id lookup within a generation (`group_rows.go`);
  - fetch by `_id`.
- Edges:
  - walk from a known resource, filtering label and target type (`navigation_render.go`,
    `category_related_scan.go`);
  - whole-generation counts grouped by `from_type, label, to_type` (`catalog/arango/relationships.go`,
    `source_load_copy.go:542`);
  - generation plus `source_file` checks.

**Designed set:**

| Collection | Indexes | Replaces |
|---|---|---|
| Each resource collection | primary; `{project, dataset_generation, id}` with stored value `auth_resource_path` | 10 persistent indexes |
| `fhir_edge` | primary, edge index; `{project, dataset_generation, from_type, label, to_type}` with stored values `auth_resource_path, source_file` | 7 label/type indexes |
| `fhir_edge` (only if measured to help) | `{_from, label, to_type}`, `{_to, label, from_type}` | 2 five-field `_from`/`_to` indexes, whose project/generation fields are already part of `_from`/`_to` |
| Preview covering indexes | drop the constant `resourceType` field; move `auth_resource_path` to a stored value | — |

**Trade-offs being accepted, and checked:**

- **Auth path as a stored value.** Fine when a generation has one auth path. If repository loads mix
  auth paths, a restricted query reads the generation's whole index range instead of its slice. If
  step 2 finds mixing, keep `auth_resource_path` as the fourth indexed field.
- **One `from_type`-led edge index.** A query filtering `to_type` or `label` without `from_type`
  would scan the generation's index range. Step 1 looks for such queries.
- **Indexes without a generation field go away.** Any query that doesn't filter on
  `dataset_generation` needs a rewrite or one justified index (step 2).
- **Dropping the per-vertex edge indexes** slows walks from heavily connected resources. They are
  kept unless measured unnecessary.
- **Bootstrap only adds indexes**, so existing databases keep the old indexes, and their write cost,
  until those are dropped explicitly. Fresh databases (the new-user case, and every benchmark run)
  get the gain immediately.

**Steps:**

1. **List the query set and check it with Arango's query planner.** The set:
   - dataframe compiler renders from the conformance and recipe fixtures;
   - `graph_service.go`;
   - explorer;
   - catalog/relationship/evidence queries;
   - source-load copy and cleanup.

   Load C0's data into a database that has only the designed set, run every query through
   `/_api/explain` (parse with `internal/store/arango/explain.go`), and confirm:
   - each pattern uses its intended index;
   - edge counts read no edge documents;
   - edge walks use the per-vertex indexes when they exist;
   - no query filters `to_type` or `label` without `from_type`.
2. **Audit queries without a generation filter** (single-resource `resource_load.go`, explorer,
   remaining GraphQL). Rewrite each one to filter on generation, or record one justified index.
   Check with one query whether any generation holds more than one `auth_resource_path`.
3. **Time the query set** (warm p50/p95) against C0. A query more than 10% slower is fixed first by
   a query rewrite or a field-order change, and only as a last resort by an added index. Keep the
   per-vertex edge indexes only if walks from the highest-degree resources regress more than 10%
   without them.
4. **Change bootstrap** to the final set. Write `docs/ARANGO_INDEX_USAGE.csv` (index → the query ids
   that justify it). Add an integration guard test:
   - every bootstrap index is in the contract;
   - every contract query uses its planned index;
   - edge counts are answered from the index alone.

## Track C: Batch size and writer count (after A and B merge; one script run)

1. With A and B merged, run batch sizes 5k / 10k / 20k with 8 writers, then writers 4 / 8 / 12 at
   the best batch size. One run per point. This is a screening pass: rerun the top two points 3×
   each before picking.
2. Change the defaults in `internal/ingest/load_options.go` only if the winner beats the current
   defaults by more than run-to-run noise (the spread of C0's three runs). Add a separate edge batch
   size only if edges and vertices clearly prefer different sizes.
3. If the server metrics showed RocksDB write stalls, try raised write-buffer settings in the
   compose files once. Keep them only if they win, and record the memory cost.

## Integration and verification

- **Parallel work.** Track A and Track B are separate worktrees, owners, and commits, and they can
  run at the same time. Any experiments that need Arango go through the one runner, one load at a
  time.
- **Before each track's commit:**
  - its tests;
  - `make test-go` and `make lint-go-comments`;
  - `make test-integration`;
  - a `pstack:interrogate` review of the exact diff, per AGENTS.md.
- **Integrate A, then B, then C.** After A and after B, take one quick 1-run Arango reading as a
  sanity check. The real numbers come from the final rerun.
- **Final acceptance:** the `verify-loom` skill on the final tip, including ClickHouse publication
  (still unverified from round 1).

## Final rerun (apples to apples)

- **Same conditions as C0:** host, dedicated Arango container and flags, other stacks stopped,
  valid-subset digest, Go 1.27.4, default options (or the new defaults from Track C, reported both
  ways), `GOGC` unset, fresh database per run.
- **Run order:** C0, then after-A, after-B, and final binaries, interleaved round-robin, 3 rounds.
  Report medians. If C0 drifts more than 10% in a round, discard and rerun that round.
- **Table columns:**
  - wall time;
  - Δ vs C0;
  - Δ vs previous snapshot;
  - insert worker-seconds per class;
  - queue-wait worker-seconds;
  - documents and index count per class;
  - request bytes;
  - p95 request time;
  - finalization stage times;
  - Arango CPU;
  - RocksDB stalls.
- **Equivalence checks on the final tip vs C0:**
  - vertices and edges identical after normalization;
  - inventory entries pass Track A's equality test;
  - the index guard test passes;
  - query p95 within 10%.
- **Say what is unverified:** for example other datasets, and reloads into existing databases that
  still carry the old indexes.

## Not doing now

| Item | Why not now |
|---|---|
| Deferred index builds | Little gain with 1–2 indexes per collection |
| Shorter document keys | Small expected gain; touches identity code; revisit if edges still dominate after this round |
| CPU floor (`field_profile` 120 worker-s) | Matters once writes stop dominating; next round |
| Command to drop old indexes in existing databases | Needed for existing deployments to get the gain, not for new users or the benchmark; follow-up |
| Cap on lazily created preview indexes | Affects reloads, not first loads; measure in a dev database first |
| Retained backfill writing entries directly | Its resumable scans need a checkpoint redesign |
