# Luna multi-agent execution plan: AQL runtime round 4

## Mission

Reduce the real GDC dataframe operation from its current roughly 5.7-second
Arango execution median to **1–3 seconds for 1,000 rows**, while preserving
generic FHIR semantics, authorization, generation isolation, deterministic
output, and bounded memory.

This round is runtime-first. A memory reduction is valuable, but it does not
qualify as the principal result of a work package unless runtime also improves.
Compiler construction time, GraphQL input mapping, AQL planning, query
execution, row transfer, JSON materialization, and optional export are measured
separately so frontend turnaround can be predicted honestly.

The target represents the first execution of a newly requested dataframe
shape. Do not use Arango's result cache to meet it. Repeated identical queries
may be reported as additional evidence, but cached results are not the product
SLO.

Read these files completely before starting any package:

- `docs/AQL_EXECUTION_IMPLEMENTATION_AUDIT.md`
- `docs/LUNA_AQL_EXECUTION_ROUND_3.md`
- `docs/benchmarks/round3/WP10_REPORT.md`
- `docs/benchmarks/round3/wp4/production.aql`
- `docs/benchmarks/round3/wp4/production.json`

## Invariant request and current baseline

Every promotion decision uses the actual frontend input path:

- GraphQL document: `examples/meta_gdc_case_matrix.graphql`
- GraphQL variables: `examples/meta_gdc_case_matrix.variables.json`
- mapping: `graphqlapi/dataframe.BuilderFromInput`
- project: `ARANGODB_PROTO`
- database: `fhir_proto`
- root limit: 1,000

The compiler fixture `conformance/compiler/fixtures/gdc-case-matrix.json` is
useful for unit coverage but is not interchangeable with the real GraphQL
input and cannot provide promotion evidence.

Current production facts:

| Measure | Value |
|---|---:|
| result SHA-256 | `17faea7ac3ee7f308b37223f376530a0660f8068d5e015cc573cf99ccb4045ca` |
| production AQL SHA-256 | `4081527f4d893c7fc8b4957ad75ffbf51a975a8b646c315f01d09093444aad68` |
| five-run Arango executing median | approximately 5.67s |
| indexed items | 475,876 |
| full scans | 0 |
| peak memory | 194,117,632 bytes |
| native traversals | 4 |
| native traversal edge index | default `fhir_edge(_to)` |
| child-set materializations | 6 typed sets plus one broad shared set |
| child-set sorts | 7 plus three representative-slice sorts |
| repeated projected-set consumer loops | at least 7 |

The root index and root limit are already effective. The remaining work is
dominated by traversal adjacency, correlated child materialization, selector
enumeration, deduplication, sorting, aggregate/pivot/slice consumers, and final
row construction.

## Product SLO and promotion thresholds

### Primary target

- exact 1,000-row result;
- five alternating, non-result-cached candidate/control runs;
- candidate Arango `executing` median between 1 and 3 seconds;
- end-to-end GraphQL median below 4 seconds when the local API is available;
- peak Arango memory at or below 200 MB;
- zero full collection scans; and
- no authorization, generation, ordering, null, pivot, slice, or duplicate-edge
  semantic drift.

### Incremental package gate

A package can merge before the final target only if it produces one of:

1. at least 10% lower whole-query Arango median; or
2. at least 25% lower target-region indexed/items/calculation work and at least
   5% lower whole-query median; or
3. at least 15% lower end-to-end time for a previously dominant non-Arango
   stage, without moving work out of the measured boundary.

Memory must not regress by more than 10%. A candidate that improves runtime by
20% or more may use up to 225 MB temporarily, but it must carry an explicit
memory follow-up before default enablement.

### Stop rule

Reject a package when it changes only AQL text, estimated cost, or optimizer
diagnostics without removing measured runtime work. Do not retain alternate IR,
renderer branches, environment switches, or indexes for rejected candidates.

## Benchmark protocol

WP0 owns the protocol. Every worker consumes its artifacts unchanged.

1. Record Arango version, edition, CPU allocation, container memory, database
   document/edge counts, active index definitions, index cache state, and Loom
   git/worktree hashes.
2. Disable query result caching for benchmark requests. Record whether plan
   caching exists and whether the first execution populated it.
3. Alternate control and candidate: `C1, N1, C2, N2 ... C5, N5`. Do not run all
   controls and then all candidates.
4. Use identical binds, limit, cursor batch size, database state, and output
   consumption. Consume every result row.
5. Record both Arango `PROFILE` and ordinary query timings. `PROFILE` is not a
   substitute for client wall time.
6. Record AQL/result hashes, response rows, uncompressed JSON bytes, compressed
   bytes if relevant, and serialization throughput. If output transfer alone
   exceeds the SLO, report that ceiling explicitly.
7. Record phase times: input mapping, semantic plan, physical plan, rendering,
   Arango parsing/planning/executing/finalizing, cursor transfer, Go row
   materialization, GraphQL result assembly, and optional NDJSON/Elasticsearch
   export.
8. Record selected indexes, optimizer rules, scanned full/index, document
   lookups, peak memory, calls/items/filtered/runtime for traversal, index,
   calculation, list, collect, sort, limit, and return nodes.
9. Count rendered native traversals, explicit edge loops, `LET` child arrays,
   `UNIQUE`, `COLLECT`, `SORT`, projected-set consumer loops, selector
   subqueries, and retained payloads.
10. Preserve raw AQL and raw profile JSON under the package evidence directory.

The profile comparator must not sum cumulative nested node runtimes. Use the
Arango executing phase and client wall clock for whole-query decisions.

## Global semantic contracts

Every candidate preserves:

1. one row per root and exact ascending root `_key` order;
2. project, dataset-generation, authorization, and required root membership
   before root `SORT/LIMIT`;
3. optional child shaping after the root window;
4. project, exact generation, and authorization checks on every edge and node;
5. routes exclusively from `resolveStorageRoute` and generated `fhirschema`;
6. inbound and proven outbound behavior, including
   `ResearchSubject -> ResearchStudy`;
7. bind-backed user values and validated collection/index metadata;
8. duplicate-edge node identity semantics;
9. exact output columns, values, types, null/empty behavior, array ordering,
   `SORTED_UNIQUE`, pivot collision selection, and slice sort-before-limit;
10. deterministic fallback when an index, statistic, server capability, or
    specialized strategy is unavailable; and
11. no production branch on GDC, `Patient`, `child_set_N`, example aliases, or
    example field names.

## Shared-file ownership

Only the coordinator may edit these shared production files:

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_cost.go`
- `internal/dataframe/physical_diagnostics.go`
- `internal/dataframe/physical_helpers.go`
- `internal/dataframe/physical_execution.go`
- `internal/dataframe/compile.go`
- `internal/dataframe/storage_route.go`

Experiment workers may add isolated `*_experiment_test.go` files and evidence
under `docs/benchmarks/round4/<wp>/`. They stop and hand off a typed IR proposal
when production changes are required.

WP0 alone may edit `cmd/dataframe-profile/`, `cmd/dataframe-query/`, and the
benchmark targets in `Makefile`. Index-definition changes are owned by the
index coordinator and require an explicit before/after index inventory; no
worker may create or remove indexes implicitly from a test.

## Parallel execution graph

```text
Wave 0, serialized:
  WP0 benchmark integrity and capability lock

Wave 1, four independent experiments (maximum three workers concurrently):
  WP1 native traversal OPTIONS matrix
  WP2 explicit endpoint full-query substitution
  WP3 identity-first dedup-before-shaping
  WP4 selector/expression lowering

Wave 2, serialized coordinator production merges:
  WP5 traversal strategy production integration (WP1/WP2 winners only)
  WP6 identity-first set production integration (WP3 winner only)
  WP7 expression lowering integration (WP4 winner only)

Wave 3, three parallel architectural experiments against the new baseline:
  WP8 leaf summary pushdown
  WP9 batch-root/set-oriented execution
  WP10 output materialization and export throughput

Wave 4, serialized:
  WP11 production integration and structural cost policy
  WP12 combined 1–3 second gate, cleanup, and report
```

Wave 1 workers do not wait on each other. With four total agent slots, keep the
coordinator free and start WP1, WP2, and WP3 first; start WP4 when the first
worker slot becomes available. This is a scheduling constraint, not a data
dependency. Wave 2 is serialized because each merge changes the physical
baseline. Wave 3 starts only after all accepted Wave 2 changes are re-profiled
together. In Wave 3, WP8, WP9, and WP10 may occupy all three worker slots while
the coordinator protects shared production files.

## Official Arango references

Workers must verify syntax and version support against the server reported by
WP0. These are the primary references for this round:

- query optimization and optimizer rules:
  <https://docs.arangodb.com/3.12/aql/execution-and-performance/query-optimization/>
- traversal execution options, including `parallelism`, `maxProjections`,
  `indexHint`, and `useCache`:
  <https://docs.arangodb.com/3.12/aql/graphs/traversals/>
- profiling and execution-node statistics:
  <https://docs.arangodb.com/3.12/aql/execution-and-performance/query-profiling/>

The optimizer is cost-based, but it cannot be credited with inventing Loom's
semantic batching, selector reuse, identity-first shaping, or consumer fusion.
Those transformations must be represented explicitly and proven by profiles.

## WP0 — benchmark integrity and Arango capability lock

**Owner:** coordinator. **May edit:** profiling commands, benchmark Makefile
targets, focused profiling tests, and `docs/benchmarks/round4/wp0/`.

### Tasks

1. Prove the benchmark command uses `BuilderFromInput` on the actual variables
   file. Include the effective semantic/physical plan hash in the report.
2. Query and record the exact Arango server version. Mark support for traversal
   `indexHint`, `parallelism`, `maxProjections`, `useCache`, collection index
   hints, stored values, query plan cache, and result-cache controls.
3. Inventory all `fhir_edge` indexes with name, ID, fields, direction
   applicability, selectivity, cache configuration, stored values, and size.
4. Verify inbound compound coverage begins with `_to` and outbound coverage
   begins with `_from`. Record missing symmetric indexes without creating them.
5. Add alternating control/candidate execution and output-byte accounting to
   the profile harness.
6. Add `-cache=false`, cursor batch size, ordinary-run count, and raw artifact
   directory flags where the Arango API supports them.
7. Capture five alternating baseline pairs to quantify normal variance.
8. Measure compilation separately. If semantic+physical+rendering exceeds
   100ms, create a compiler-only follow-up; do not mix it into AQL execution.
9. Measure uncompressed result bytes and local cursor transfer throughput. State
   whether 1–3 seconds is physically plausible for the current output size.

### Acceptance

- identical AQL/result hashes across unchanged runs;
- median pair variance below 10%, or a documented environment fix;
- exact Arango capability and index inventory;
- result cache proven disabled; and
- raw baseline artifacts in `docs/benchmarks/round4/wp0/`.

## WP1 — native traversal OPTIONS matrix

**Purpose:** determine whether Arango's native traversal can use existing
vertex-centric indexes and multiple cores without switching to explicit joins.

**Owner:** traversal-options worker. **May edit:** isolated experiment tests
under `internal/dataframe/` or `internal/store/arango/` and
`docs/benchmarks/round4/wp1/`. No shared production files or indexes.

### Candidate matrix

Test one traversal region at a time, starting with the most expensive nested
region. Compare:

1. current native traversal;
2. native plus compound vertex-centric `indexHint`;
3. native plus `parallelism` values 2, 4, and 8 bounded by available CPUs;
4. native plus index hint and each useful parallelism value;
5. useful candidates with `maxProjections` values 5, 8, and an explicit full
   document threshold; and
6. `useCache` true/false only to diagnose cache pollution, not as the primary
   speed mechanism.

### Tasks

1. Obtain edge collection, direction, route discriminator, and expected index
   from validated route/index metadata. Hard-coded index names are allowed only
   in isolated AQL probes and must be replaced by a typed metadata contract in
   the handoff.
2. Test inbound and outbound index-hint syntax separately. Confirm the index is
   eligible and actually selected by `EXPLAIN`; a hint is not assumed to be
   forced.
3. Test the root shared multi-type traversal and each nested single-type route.
   Multi-type `IN` and equality must have separate evidence.
4. Preserve all edge/node scope and deterministic post-traversal order.
5. Record CPU utilization and whether parallelism reduces wall time or merely
   increases contention/memory.
6. Run isolated region tests and the full actual GDC query. Region-only wins do
   not promote a production default.

### Stop conditions

- reject a hint if `EXPLAIN` still selects only the default edge index;
- reject parallelism if whole-query runtime or memory regresses;
- do not use `PRUNE` to change a depth-one result filter;
- do not force an index unsupported by the active server version; and
- stop if dynamic edge collection/index metadata cannot be represented without
  a shared IR change.

### Acceptance and handoff

Pass only a generic structural strategy meeting the incremental gate on the
full query. Hand off traversal option fields, capability checks, index metadata
requirements, exact AQL, and default/fallback policy.

## WP2 — explicit endpoint full-query substitution

**Purpose:** determine whether explicit indexed edge equality beats native
traversal in the actual correlated dataframe plan.

**Owner:** endpoint worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round4/wp2/`. No shared production files or indexes.

### Tasks

1. Begin from the exact WP0 production AQL/binds. Replace exactly one nested
   traversal region at a time with explicit endpoint equality.
2. For INBOUND, filter edge `_to == parent._id` plus project, generation, label,
   and `from_type` equality before `DOCUMENT(edge._from)` or an indexed node
   join. OUTBOUND uses `_from`, `to_type`, and `_to`.
3. Compare `DOCUMENT(endpoint)` with a primary-index collection join. Record
   document lookups and memory for both.
4. Preserve auth on both edge and node, node type verification, duplicate-edge
   identity, child filters, sorting, and compact projection.
5. Test the three actual nested GDC routes, then combinations of two and all
   three. Region interactions must be measured; isolated percentages cannot be
   added together.
6. Test shared explicit `IN` only as a control. Previous evidence shows it can
   be fast while still missing the compound index.
7. Run five alternating full-query pairs for every promotable combination.

### Acceptance and handoff

Pass when endpoint equality selects the compound index, exact parity holds,
and the full query meets the incremental gate without unacceptable memory.
Hand off a typed native/explicit strategy proposal and supported route classes.

## WP3 — identity-first deduplication before shaping

**Purpose:** stop applying object-level `UNIQUE` to identity-plus-selector
objects containing arrays.

**Owner:** identity worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round4/wp3/`. No shared production files.

### Candidate shapes

Compare the current shape against:

1. deduplicate scoped nodes by `_id` before selector extraction, then sort and
   shape;
2. `COLLECT node_id = node._id INTO group` followed by deterministic node
   selection and shaping;
3. `RETURN DISTINCT node._id` followed by primary lookup and shaping; and
4. identity-key object projection followed by selector projection.

### Tasks

1. Prove scope runs before identity deduplication.
2. Use duplicate-edge fixtures where one node appears through multiple edges.
3. Preserve one stable node document per `_id`; never deduplicate solely by
   payload or shaped object equality.
4. Sort after any operation whose output order is unspecified. Do not rely on
   traversal, `UNIQUE`, or `COLLECT` order.
5. Apply one region at a time and report removed object width, calculation
   nodes, collect/unique work, memory, and whole-query runtime.
6. Include shared subsets, nested sets, empty sets, auth/generation, and
   outbound routes.

### Acceptance and handoff

Pass only the identity operation that removes measured work and satisfies the
incremental gate. Hand off explicit identity/order properties and invalidation
rules; do not propose general sort removal.

## WP4 — selector and expression lowering

**Purpose:** reduce calculation/list-node work caused by generic singleton
selector subqueries and repeated selector enumeration.

**Owner:** expression worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round4/wp4/`. No shared production files.

### Tasks

1. Inventory every selector expression in the actual AQL and classify:
   - direct scalar path;
   - optional scalar path;
   - fixed index;
   - repeated array path;
   - predicate-bearing selector;
   - fallback chain; and
   - derived/nested object expression.
2. Compare current `FOR __root IN [payload]` lowering against direct attribute
   access for schema-proven scalar selectors.
3. Compare conditional array expansion against current nested subqueries for
   schema-proven repeated paths.
4. Compute each selector union once per shaped child item and verify every
   consumer reads the same field. Count remaining projected-set loops.
5. Test common-subexpression `LET` placement within the child loop versus outer
   correlated expressions.
6. Preserve missing/null, array, fallback, filter quantifier, primitive type,
   and FHIR choice-field behavior using `fhirschema`; no path heuristics.
7. Measure calculation/list nodes and whole-query time. Reduced AQL length is
   not evidence.

### Acceptance and handoff

Pass only schema-proven lowering families meeting the incremental gate across
the protected selector corpus and actual query. Hand off typed selector
execution modes, never raw AQL fragments.

## WP5 — production traversal strategy integration

**Owner:** coordinator. **Depends on:** WP1 and/or WP2 passing.

### Tasks

1. Add typed traversal execution fields for native options and/or explicit
   endpoint lookup. Store no raw AQL.
2. Validate direction, endpoint, discriminator, collection, index metadata,
   server capability, and fallback strategy.
3. Render only experiment-approved route classes.
4. Add deterministic rule ablation and diagnostics showing selected strategy,
   index expectation, parallelism, estimated/observed fan-out, and rejection
   reason.
5. Preserve required matches and unsupported traversal forms on their existing
   correct path.
6. Re-run actual full-query parity/profile after each accepted strategy rather
   than merging all changes before measurement.

### Acceptance

The production execution path must reproduce the experiment's full-query win,
selected index, memory bound, and all semantic tests.

## WP6 — production identity-first set integration

**Owner:** coordinator. **Depends on:** WP3 passing.

### Tasks

1. Add typed identity/dedup/order requirements to `PhysicalSet`.
2. Apply scope before dedup, dedup before selector projection, and explicit sort
   after any order-invalidating operation.
3. Render only the winning dedup shape.
4. Diagnose identity key, dedup strategy, order proof, shaped width avoided,
   and retained fallback.
5. Add duplicate-edge, auth/generation, nested, outbound, and rich-shape parity
   tests plus full-query ablation.

### Acceptance

Production must reproduce WP3's runtime improvement. Unknown identity/order
properties retain the current conservative behavior.

## WP7 — production selector/expression integration

**Owner:** coordinator. **Depends on:** WP4 passing.

### Tasks

1. Add typed selector execution modes derived from generated schema metadata.
2. Render direct scalar/array access only for proven-safe selector shapes.
3. Retain generic subquery lowering for predicates, fallbacks, unsupported
   choices, and unknown cardinality.
4. Validate mode/path/cardinality consistency in physical IR.
5. Add diagnostics and ablation for each lowering family.
6. Run the generic selector conformance corpus and actual full-query profile.

### Acceptance

Each enabled family independently passes parity and the incremental gate. Do
not default-enable a combined family whose individual contribution is unknown.

## WP8 — leaf summary pushdown

**Purpose:** replace repeated aggregate, pivot, and slice scans over one shaped
leaf set with one summary-producing subquery.

**Owner:** summary worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round4/wp8/` until coordinator promotion.

### Tasks

1. Identify leaf sets with no navigated descendants and no unsupported escaping
   consumer.
2. Deduplicate identity once, then compute named outputs in one correlated
   summary contract:
   - count;
   - count distinct/distinct values/min/max/first/exists;
   - bounded pivot pairs and collision reduction; and
   - representative slice with exact predicate, sort, tie-break, limit, and
     projection.
3. Compare one summary object against current independent loops. Do not create
   a larger unbounded intermediate array.
4. Test each operation alone, compatible mixtures, incompatible predicates,
   empty/high-fanout sets, duplicate edges, and auth/generation.
5. Apply to the actual diagnosis, sample, file, group-file, and observation
   sets; report incremental wins per source.
6. Count eliminated child-set enumerations and calculation nodes.

### Acceptance and handoff

Require at least 10% additional whole-query improvement from the post-Wave-2
baseline. Hand off a typed `PhysicalSetSummary` proposal with named outputs and
fallback reasons.

## WP9 — batch-root/set-oriented execution

**Purpose:** replace 1,000 correlated root-by-root child pipelines with batched
edge/node work over the complete root window.

**Owner:** batch worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round4/wp9/` until coordinator promotion.

### Candidate shape

1. materialize the scoped, sorted, limited root window once;
2. create a root identity set;
3. retrieve scoped edges for all root IDs using indexed endpoint access;
4. join scoped nodes and retain root identity;
5. deduplicate/shape/group by root identity; and
6. left-join summaries back to the root window in exact root order, preserving
   roots with no optional children.

### Tasks

1. Compare per-root equality, batched `IN`, chunked root IDs, and grouped edge
   scans. Record whether compound indexes remain selected.
2. Test root batch sizes 25, 100, 250, 500, and 1,000. Bound intermediate
   cardinality and memory.
3. Start with one relationship and one output, then add nested paths. Attribute
   each step.
4. Preserve optional-left-join semantics, root ordering, duplicate-edge
   identity, auth/generation, and outbound direction.
5. Compare batch execution to the best accepted correlated plan, not Round 3.
6. Reject any shape that requires loading all project edges before the root
   limit or loses the endpoint compound index.

### Acceptance and handoff

Require at least 20% additional whole-query improvement at 1,000 rows and no
more than 5% regression at 25/100 rows. Hand off typed batch/subplan/grouping
IR only after parity and memory pass.

## WP10 — output materialization and export throughput

**Purpose:** ensure the frontend-visible turnaround is not dominated by cursor
transfer, JSON assembly, or export after AQL reaches the target.

**Owner:** runtime pipeline worker. **May edit:** isolated benchmark commands,
tests, and `docs/benchmarks/round4/wp10/`; no compiler semantics.

### Tasks

1. Measure AQL execution, cursor fetch, RawMessage decoding, GraphQL assembly,
   response encoding, NDJSON/CSV encoding, and Elasticsearch bulk-body creation
   separately for the exact 1,000 rows.
2. Record response bytes and rows/MB per second.
3. Compare cursor batch sizes without changing query results or memory bounds.
4. Test streaming rows directly into NDJSON/Elasticsearch bulk format instead
   of retaining the full result in Go, if the existing API boundary permits it.
5. Do not hide AQL work in asynchronous background processing when measuring
   "dataframe created". Report accepted/queued and fully-created latency
   separately if a job API is proposed.
6. Do not use response compression to claim lower server computation time;
   report transport benefit separately.

### Acceptance

The non-Arango pipeline should add less than one second for 1,000 rows on the
local development server, or produce a concrete throughput/output-size limit
that changes the product SLO.

## WP11 — production integration and cost policy

**Owner:** coordinator. **Depends on:** passing WP8 and/or WP9 plus accepted
Wave-2 production changes.

### Tasks

1. Add only experiment-proven summary/batch IR.
2. Integrate accepted strategies sequentially and re-profile after each.
3. Carry route cardinality, root limit, projected width, fan-out, server
   capability, and available index metadata into deterministic structural
   choices.
4. Statistics may choose only among semantically equivalent proven strategies.
   Unknown statistics use the safest measured fallback.
5. Emit a decision trace suitable for frontend/admin diagnostics without
   exposing authorization values.
6. Remove superseded prepared-array, rejected alternate renderers, stale rule
   switches, and test-only production hooks.

### Acceptance

Combined production AQL reproduces individual wins, exact parity, and the
memory gate. If two individually useful rules regress together, keep the faster
combination and remove the loser.

## WP12 — 1–3 second final gate and cleanup

**Owner:** coordinator only.

### Tasks

1. Capture fresh alternating controls for the final production policy and every
   accepted rule ablation at limits 25, 100, and 1,000.
2. Run the full Go suite, conformance/compiler suite, live Arango parity,
   Explain/index, auth/generation, duplicate-edge, deep traversal, required
   match, inbound, outbound, aggregate, pivot, slice, fallback, and derived
   field cases.
3. Save final AQL, raw profiles, result hashes, response bytes, end-to-end
   timings, selected indexes, CPU, and memory.
4. Delete rejected experiments from production code and update all stale
   completion claims.
5. Report contribution by rewrite; do not attribute the combined win to all
   packages equally.

### Final decision

The round succeeds when the actual 1,000-row request has:

- five-run non-cached Arango median at or below 3.0 seconds;
- stretch median near 1.0–2.0 seconds;
- exact canonical result hash;
- zero full scans;
- peak memory at or below 200 MB; and
- end-to-end local API median below 4.0 seconds.

If execution remains above three seconds, the final report identifies the
largest remaining node family and answers whether the limit is query shape,
edge/storage layout, output size, or available CPU. Do not close the round with
"more optimization may be possible."

## Luna worker prompt template

```text
Execute <WP number and title> from docs/LUNA_AQL_RUNTIME_ROUND_4.md.

Read the Round 4 plan, AQL implementation audit, Round 3 plan, Round 3 final
report, and every file named by this package completely. Own only <exact
paths>. Do not edit shared compiler files unless designated coordinator.

Before editing, record git status and SHA-256 hashes of owned existing files.
Preserve unrelated dirty changes. Use apply_patch and prefix shell commands
with rtk. Use the real examples/meta_gdc_case_matrix.variables.json through
BuilderFromInput for performance evidence; compiler fixtures are unit coverage
only. Use fhirschema and resolveStorageRoute and never hard-code a FHIR type,
route, child_set variable, example alias, or index name in production.

Run WP0's alternating, cache-disabled baseline first. Implement only this
package. Preserve every global semantic contract. Run named unit/live tests and
write raw AQL/profile evidence under docs/benchmarks/round4/<wp>/.

Report changed files and hashes, exact commands, five raw alternating times,
medians, AQL/result hashes, response bytes, scanned items, document lookups,
peak memory, selected indexes, top profile nodes, structural work removed,
rejected candidates, and enable/cost-gate/reject decision.

Stop rather than guess if an unowned IR change is required, an owned file
changes concurrently, the result hash differs, the intended index is not
selected, scope/order semantics change, server capability is absent, output is
not fully consumed, or the required runtime benefit is missing.
```
