# Luna High execution plan: AQL optimization round two

## Mission

Reduce Loom's warm rich-dataframe AQL time without changing results,
authorization, generation isolation, supported FHIR routes, or GraphQL. The
physical compiler is the only production path. Do not restore compatibility
code or add resource-specific optimizer rules.

Current live GDC baseline over loaded `META`, database `fhir_proto`, project
`ARANGODB_PROTO`:

| Measure | Baseline |
|---|---:|
| rows / response | 1,000 / 3,303,295 bytes |
| mean / warm HTTP | 6.364s / 6.339s |
| Arango query | 6.301s |
| warm preparation / compilation | 0.075ms / 1.212ms |
| row materialization / HTTP overhead | 1.748ms / about 35ms |
| traversal sets / eliminated traversals | 4 / 2 |

The warm bottleneck is Arango, not Go compilation. The first request still
spends about 10.75s in relationship discovery; WP7 addresses that separately.

Baseline command:

```bash
make dataframe-demo DATAFRAME_LIMIT=1000 DATAFRAME_REPEAT=3 DATAFRAME_PRINT_RESPONSE=false
```

## Read first

- `docs/AQL_OPTIMIZATION_WORKLIST.md`
- `docs/benchmarks/AQL_PROFILE_CORPUS.md`
- `conformance/compiler/fixtures/gdc-case-matrix.json`
- `examples/meta_gdc_case_matrix.graphql`
- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_prefix.go`
- `internal/dataframe/physical_cost.go`
- `internal/dataframe/profile.go`
- `internal/store/arango/profile.go`
- `internal/ingest/backend.go`

Already complete: generic physical lowering, scoped graph traversal, root
windowing, required semi-joins, sibling sharing, aggregates, pivots, slices,
nested objects, prepared sets, structural cost reporting, opt-in profile, and
compatibility renderer deletion. Do not rebuild these foundations.

## Global contracts

Every package preserves:

1. One row per root in stable `_key` order.
2. Columns, row count, null/empty behavior, array order, distinct behavior,
   pivot collision behavior, and representative slice choice.
3. Required matching before root `SORT/LIMIT`; optional shaping after it.
4. Project, generation, and authorization checks on every root, node, and edge.
5. Bind-backed request values and names; no request interpolation into AQL.
6. Routes accepted by `resolveStorageRoute` only.
7. Generic physical-property rules only: no production Patient, Specimen,
   Observation, GDC, or fixture-label special cases.
8. Disable/remove a rule when parity is unproven or live profile is slower.

## Evidence required from every optimization

Publish fixture/data scope, limit, batch size, complete rule policy, canonical
result SHA-256, rows, response bytes, AQL hash, every execution time, five-run
warm median/minimum, profile work counters, peak memory, top ten runtime nodes,
selected indexes, and a conclusion: enable, cost-gate, disable, or remove.

Object key order must not affect hashes; array order must. Compare identical
database state and binds while changing one rule only.

## Shared-file ownership

Only the coordinator merges edits to:

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_cost.go`
- `internal/dataframe/physical_diagnostics.go`

Workers prepare tests/reports/proposed patches. Never let two workers edit
these files concurrently. Maintain independent developer switches for current
sharing, nested sharing, prepared selectors, rich fusion, and compact set
projection.

## WP0 — Reproducible baseline and profile attribution

**Owner:** benchmark worker. **Owns:** `cmd/dataframe-query/`, conformance,
examples, and benchmark docs. No physical compiler edits.

Implement:

1. Compile a fixture, execute it, then `EXPLAIN` and profile level 2 using the
   same AQL and binds.
2. Support limits 25/100/1,000 and repetitions 1/5.
3. Canonicalize JSON results and calculate SHA-256.
4. Attribute profile nodes to root window, every set/prepared set, aggregate,
   pivot, slice, and return.
5. Record scalar-root, optional-child, aggregate+slice, pivot, deep traversal,
   sibling, required-match, and GDC baselines.
6. Store optimizer policy and AQL hash in every artifact.

Acceptance: repeated hashes match; GDC returns 1,000 rows and approximately
3.3MB; top nodes map to physical regions; `go test ./conformance/compiler
./cmd/dataframe-query -count=1` passes. Handoff artifact schema, fixture IDs,
hashes, commands, and the three most expensive regions.

## WP1 — Independent optimizer-rule ablation

**Owner:** coordinator. **Depends on:** WP0.

Implement:

1. Typed independent decisions for current sharing and prepared selectors;
   disabled entries for nested sharing, fusion, and compact projection.
2. Keep `CompileRequest` production-only; add internal/test
   `CompileRequestWithPolicy` rather than environment-only control.
3. Report each rule's state, estimate, and rejection reason.
4. Compile the same request while changing exactly one rule.
5. Label every artifact with the full policy.

Acceptance: live GDC hashes match for no optional rewrites, sharing only,
prepared only, and defaults; `go test ./internal/dataframe
./conformance/compiler -count=1` passes. WP1 enables no new rule.

## WP2 — Traversal fan-out and nested-prefix optimization

**Owner:** traversal worker; coordinator merges shared files. **Depends on:**
WP0/WP1.

Investigate before coding:

1. Profile sibling sharing on/off for focused fixtures and GDC.
2. Per traversal record parents, edge-index items, node lookups, filtered and
   returned items, and runtime.
3. Compare broad multi-type traversal with independent typed traversals.
4. Test whether `POSITION(@target_types, edge type, true)` changes index choice
   versus equality.
5. Profile deep paths separately.
6. Find repeated nested prefixes using `DecomposePhysicalTraversalPrefix`.

Candidate A: cost-aware sibling sharing. Estimate union-neighbor versus
independent typed work from catalog/profile counts; reject broad sharing when
it loses selectivity; report exact inputs/reason.

Candidate B: nested sharing, only with profile evidence. Require equal parent,
direction, label, scope, and optionality; alpha-rename captures; materialize in
the same parent scope; derive consumer subsets; never move a semi-join after
the root window.

Tests: zero/one/many neighbors, skewed type distribution, inbound and proven
outbound routes, three-hop equivalence, and rejection for differing auth,
generation, direction, label, or parent. Enable only with identical hashes, no
full scan, reduced targeted work, and at least 5% warm-median improvement.

## WP3 — Prepared-selector cost and payload minimization

**Owner:** prepared-set worker; coordinator merges shared files. **Depends on:**
WP0/WP1. May investigate with WP2.

First profile prepared on/off for aggregate+slice, pivot, deep-child, and GDC.
Record selector calls, prepared items, memory, runtime, and hashes at child
cardinality 0/1/10/100/high-fan-out.

Implement:

1. Compute selector union for aggregate values/predicates, pivot key/value,
   slice predicate/sort, and slice fields.
2. Project each eligible selector once.
3. Add explicit typed `RetainNode`; retain full nodes only for nested traversal
   or an unprepared consumer.
4. Keep single-use selectors direct unless profile proves value.
5. Cost-gate on consumer count, child estimate, field count, and node retention.
6. Keep ordered fallback chains direct until preparation preserves all fallbacks.

Tests: direct/prepared null, empty, multi-value, distinct, slice ordering,
pivot collisions, shared-subset prepared definitions, bind correctness, and
corpus hashes. Prepared mode must improve focused fixtures and be neutral or
better for GDC; otherwise cost-gate/disable it.

## WP4 — Fuse compatible rich consumers

**Owner:** rich-expression worker; coordinator merges IR/renderer. **Depends
on:** WP1 and WP3 evidence.

Implement a typed consumer group keyed by source, identical predicate,
ordering needs, and prepared schema. Classify count/exists, distinct/min/max,
pivot grouping, and bounded slices. Group only identical semantics. Render one
typed shaping subquery and project columns from its object. Preserve
sort-before-limit, pivot allowed columns/collisions/distinctness, and lexical
scope. Keep disabled until profile passes.

Tests: identical counts fuse, different predicates do not, count+distinct,
aggregate+slice, aggregate+pivot, empty/null/duplicate/multi-value selectors,
nested scope, and live hash parity. Keep only if loops/items and focused
runtime improve without GDC regression; remove if Arango already fuses it.

## WP5 — Compact intermediate set projection

**Owner:** projection worker; coordinator merges renderer. **Depends on:** WP0.

Implement:

1. Compute required set properties from downstream selectors, endpoint
   identity, `_key`, typing, ordering, and uniqueness.
2. Define typed set output; keep document/full node only for later traversal.
3. Preserve `_key` and run scope predicates before compact projection.
4. Compare full-object `UNIQUE` with identity uniqueness only under
   duplicate-edge parity tests.
5. Measure intermediate memory separately from requested response size.

Tests: nested traversal handle, duplicate edges, ordering/slices, requested
columns, auth/generation ordering, and compact/full hashes. Require lower peak
memory or copied work and neutral/better runtime; otherwise disable/remove.

## WP6 — Profile-driven Arango index audit

**Owner:** index worker. **Owns:** `internal/ingest/backend.go`, Arango
explain/profile tests, index docs. No renderer edits. **Depends on:** WP0 and a
repeat after WP2--WP5 stabilize.

1. Inventory installed indexes, field order, selectivity, and corpus usage.
2. Verify INBOUND `_to,project,dataset_generation,label,from_type` and OUTBOUND
   `_from,project,dataset_generation,label,to_type` indexes.
3. Determine whether multi-type sharing uses compound or default edge indexes.
4. Test alternative orders only with disposable named indexes.
5. Compare equality-per-type and multi-type traversal.
6. Verify root scope plus `_key` sort avoids unrelated roots.
7. Record ingest time, index size, runtime, and scanned work. Add no speculative
   index and remove none without corpus proof.

Acceptance: checked-in shape/index matrix, no execution full scans, documented
tradeoffs, and `go test ./internal/ingest ./internal/store/arango -count=1`.

## WP7 — Ingest-time relationship catalog

**Owner:** catalog/ingest worker. **Owns:** catalog, ingest, loader command, and
related tests/docs. No physical compiler edits. May run with WP2--WP6.

Create an ingest-owned relationship catalog keyed by project, generation,
auth path, from type, label, to type, and edge count.

1. Bootstrap indexed builder lookup by project/generation/to-type and storage
   lookup by project/generation/from-type, with auth where required.
2. Count successfully committed edges, not attempted rows.
3. Define legacy rebuild and immutable-generation behavior.
4. Read discovery from catalog; retain direct edge aggregation only as explicit
   repair/backfill, never request fallback.
5. Add rebuild command for the existing 14.5-million-edge database.
6. Invalidate memory cache only after successful import/rebuild.
7. Preserve restricted authorization aggregation.

Tests: empty data, two projects/generations, restricted/unrestricted auth,
idempotent rebuild, failed writes, builder orientation, and catalog/direct
parity. Gate: cold discovery performs no edge scan, GDC preparation below
250ms, and `go test ./internal/catalog ./internal/ingest
./cmd/arango-fhir-proto -count=1`.

## WP8 — Durable parity/profile regression gates

**Owner:** conformance worker. **Depends on:** WP0 and consumes WP2--WP7.

1. Audit that every supplied bind is referenced and every AQL bind has a
   value, correctly handling `@@collection` and prefix collisions.
2. Audit physical set/prepared variable definitions and lexical uses.
3. Compare canonical hashes across WP1 ablations.
4. Add opt-in live gates for indexes, `scannedFull == 0`, rows, and hashes.
5. Use generous timing ceilings; fail primarily on structural regressions.
6. Assert discovery uses the catalog, never direct aggregation.
7. Document exact 25/100/1,000 and profile commands.

Acceptance: tests catch historical missing `child_set_1_prepared` and unused
`child_set_2_label`; corpus hashes match; `go test ./... -count=1` passes
offline; opt-in live suite passes.

## WP9 — Integrate proven rules and publish baseline

**Owner:** coordinator. **Depends on:** WP2--WP8 evidence.

Review hashes, work counters, warm median, memory, and regressions. Classify
each rule as default, shape-cost-gated, experimental, or rejected. Delete
rejected prototype code. Run full unit/conformance/live suites and
25/100/1,000 plus cold discovery benchmarks. Publish hashes, AQL hashes,
profiles, and timings.

Done: no normal execution/discovery full scans, cold preparation below 250ms,
all correctness contracts pass, and warm GDC median is below 6.34s. If it does
not improve, document the profile-proven irreducible fan-out and next physical
strategy rather than claiming success.

## Parallel waves

Wave 1, concurrent: Luna A WP0; Luna B WP6 investigation; Luna C WP7;
coordinator WP1. Merge WP0 contract before WP1 controls. WP7 is independent.

Wave 2 after WP0/WP1, concurrent without shared-file edits: Luna D WP2; Luna E
WP3; Luna F WP5; Luna B repeat WP6. Workers deliver tests/reports/proposed
patches. Coordinator integrates one candidate, profiles, retains/removes, then
takes the next.

Wave 3 after WP3 decision: Luna G WP4; Luna H WP8; Luna B final WP6;
coordinator sequential integration.

Wave 4: coordinator executes WP9 alone. No new optimizer design during final
integration.

## Copy-paste worker prompt

```text
Execute <WP number/title> in Loom.

Read docs/LUNA_AQL_OPTIMIZATION_ROUND_2.md completely, then the package files
it lists. Own only <exact paths>. Do not edit physical_plan.go,
physical_optimize.go, physical_render.go, physical_cost.go, or
physical_diagnostics.go unless designated coordinator.

Preserve every global contract. Use fhirschema and resolveStorageRoute; never
hard-code a FHIR type/route. Preserve unrelated dirty changes. Use apply_patch
and prefix shell commands with rtk.

Run baseline tests, implement only this package, run named unit/live tests,
produce the required evidence artifact, and report changed files, hashes,
profile metrics, rejected experiments, and coordinator decisions needed.

Stop rather than guess if an unowned IR change is required, hashes differ,
scope semantics change, profile benefit is absent, or an owned file changed
concurrently.
```

## Coordinator merge checklist

For each candidate: reject resource-specific logic; run focused tests and
`go test ./... -count=1`; compile optimized/ablated AQL from the same request;
verify binds and lexical variables; compare hashes at 25/1,000 rows; confirm
indexes/no full scan; compare five warm runs and profiles; record median, work,
and memory; then enable, cost-gate, or remove.

