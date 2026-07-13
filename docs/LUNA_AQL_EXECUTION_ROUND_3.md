# Luna execution plan: AQL execution optimization round 3

## Mission

Reduce the Arango execution time and retained memory of Loom's generic FHIR
dataframe AQL. Optimize the generic physical translation, not the example
schema or the Go/GraphQL request path.

This plan replaces the earlier prepared-selector and byte-identical-consumer
fusion plan. The current compiler already proved those formulations do not
justify production work:

- the second prepared array was slower and added roughly 303 MB of peak memory;
- the rich-consumer classifier found no multi-consumer group in the GDC query;
- native traversals ignore Loom's endpoint-first compound edge indexes; and
- payload-bearing sets are materialized, deduplicated, sorted, and then scanned
  again for aggregates, pivots, and slices.

The work below is ordered around removal of those measured costs. A work
package is not complete merely because it adds an IR type, optimizer rule, or
passing unit test. It must either prove a material profile improvement or be
rejected and removed.

Read `docs/AQL_EXECUTION_IMPLEMENTATION_AUDIT.md` completely before starting
any package.

## Current baseline and invariant input

The invariant benchmark is the real GraphQL request formed by:

- `examples/meta_gdc_case_matrix.graphql`
- `examples/meta_gdc_case_matrix.variables.json`
- limit 1,000
- project `ARANGODB_PROTO`
- database `fhir_proto`

Checked-in baseline artifacts:

- `docs/benchmarks/meta_gdc_case_matrix-profile.aql`
- `docs/benchmarks/meta_gdc_case_matrix-profile.json`
- `docs/benchmarks/GDC_AQL_PROFILE.md`

| Measure | Baseline |
|---|---:|
| AQL hash | `c0b39eb0ec0f29a09b1661c78fc377159881aae81214e505a5427495c8a7e07c` |
| canonical result hash | `17faea7ac3ee7f308b37223f376530a0660f8068d5e015cc573cf99ccb4045ca` |
| Arango executing phase | 5.928s |
| indexed items scanned | 475,876 |
| full scans | 0 |
| peak memory | 269,844,480 bytes |
| `UNIQUE` expressions | 8 |
| `SORT` clauses | 11 |
| native traversals | 4 |
| post-materialization set loops | 7 |
| child sets retaining payload | 6 |

The baseline numbers are reference evidence, not permission to compare results
from a different database state. WP0 must recapture a same-session baseline
before every experiment series.

## Global semantic contracts

Every work package must preserve:

1. one row per root and ascending root `_key` order;
2. root project, generation, authorization, and required filters before the
   root `SORT/LIMIT` window;
3. optional child shaping after the root window;
4. project, exact generation, and authorization checks on every traversed edge
   and node;
5. route direction, edge label, endpoint discriminator, and target type from
   `resolveStorageRoute` and generated `fhirschema` data only;
6. bind-backed values and collection binds; no interpolated user values;
7. exact null versus empty-array behavior, output column names, array order,
   `SORTED_UNIQUE`, pivot collision selection, and representative-slice
   sort-before-limit semantics;
8. duplicate-edge identity semantics and deterministic tie-breaking;
9. inbound and proven outbound traversal behavior, notably
   `ResearchSubject -> ResearchStudy`; and
10. a deterministic correct fallback when statistics or a specialized
    strategy are unavailable.

Production code must never branch on `Patient`, GDC, a `child_set_N` variable,
an example column name, or a known example edge label.

## Required evidence and promotion gate

Every experiment and production packet records:

- commit/worktree hash and hashes of every owned file before editing;
- exact command, policy environment, database, project, limit, and run time;
- rendered AQL hash and canonical result hash;
- five raw warm client and Arango execution times, median, and minimum;
- response rows and bytes;
- scanned full/index items and peak memory;
- traversal calls/items/filtered/runtime and selected index names/fields;
- top ten profile nodes by cumulative runtime and by item count;
- counts of `UNIQUE`, `SORT`, traversal, materialized-set, payload-retaining,
  and post-materialization enumeration operations;
- the structural reason the candidate should improve the plan; and
- an `enable`, `cost-gate`, or `reject` decision.

Use:

```bash
make dataframe-demo \
  DATAFRAME_LIMIT=1000 \
  DATAFRAME_REPEAT=5 \
  DATAFRAME_PRINT_RESPONSE=false

make dataframe-profile \
  DATAFRAME_PROFILE_LIMIT=1000
```

A production rewrite is promotable only when:

- result hashes match at limits 25 and 1,000;
- there are zero full collection scans;
- scope and route parity tests pass;
- the same-session five-run warm median improves by at least 10%; or the
  targeted profile region loses at least 25% of its work while the whole query
  improves by at least 5%;
- peak memory does not regress by more than 5%; and
- protected scalar-root, single-child, aggregate, pivot, slice, deep traversal,
  sibling traversal, required-match, auth, generation, and outbound cases do
  not regress by more than 5%.

An experiment with no measurable benefit is a successful rejection. Do not
leave its production switch, alternate renderer, or unused IR behind.

## Ownership and parallel execution

The following files are shared compiler files. Only the integration
coordinator may edit them:

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_cost.go`
- `internal/dataframe/physical_diagnostics.go`
- `internal/dataframe/physical_execution.go`

Experiment workers may add isolated `*_experiment_test.go` files under
`internal/dataframe/` or `internal/store/arango/`, add benchmark artifacts
under their assigned `docs/benchmarks/round3/<wp>/` directory, and extend
`cmd/dataframe-profile/` only when their package explicitly owns it. They may
not patch shared production behavior.

```text
Wave 0, serialized:
  WP0 baseline lock and parity harness

Wave 1, parallel experiments:
  WP1 endpoint/index strategy matrix
  WP3 traversal-time shaping projection
  WP6 identity/order/dedup property proof

Wave 2, serialized coordinator merges:
  WP2 endpoint-aware traversal lowering, only if WP1 passes
  WP4 traversal-time shaping, only if WP3 passes
  WP7 physical identity/order properties, only for WP6-proven changes

Wave 3, after WP4:
  WP5 leaf summary pushdown

Wave 4, conditional parallel investigations:
  WP8 batch-root execution, only if warm median remains above 3s
  WP9 catalog-backed costing, only after at least two strategies survive

Wave 5, serialized:
  WP10 integration, cleanup, and final profile
```

No worker may write a shared file while another worker or coordinator owns it.
Stop when an unowned IR change is required and hand the coordinator a typed IR
proposal instead.

## WP0 - lock baseline, parity, and profile accounting

**Purpose:** make every later speedup attributable and reproducible.

**Owner:** coordinator. **May edit:** `cmd/dataframe-profile/`, focused profile
tests, `docs/benchmarks/round3/wp0/`, and Makefile profile targets. Do not alter
compiler output.

### Tasks

1. Verify the exact GraphQL request and variables are used by both demo and
   profile commands. Record the effective limit and every bind variable without
   logging authorization secrets.
2. Capture a fresh five-run warm baseline and raw `PROFILE` result. Preserve
   raw artifacts, not only a Markdown summary.
3. Make canonical result hashing insensitive only to JSON object key order. It
   must remain sensitive to row order, array order, nulls, empty arrays, scalar
   types, and values.
4. Add or verify structural AQL accounting for the operation counts required by
   the evidence contract.
5. Add a comparison command or test that accepts baseline/candidate profile
   artifacts and reports absolute and percentage changes. It must not sum
   nested cumulative profile runtimes.
6. Run the protected generic compiler corpus once and record its commands and
   current hashes.

### Acceptance

- two unchanged consecutive captures have identical result and AQL hashes;
- their warm medians are within 10%, otherwise diagnose environmental noise;
- comparison output identifies selected edge indexes and the four current
  traversal regions; and
- no production AQL changes.

**Artifact:** `docs/benchmarks/round3/wp0/BASELINE.md` plus raw JSON/AQL.

## WP1 - endpoint/index traversal strategy experiment

**Purpose:** determine which generic traversal shape eliminates the largest
measured filtered-adjacency work.

**Owner:** experiment worker. **May edit:** new
`internal/dataframe/*endpoint_strategy_experiment_test.go`, new
`internal/store/arango/*endpoint_strategy_experiment_test.go`, and
`docs/benchmarks/round3/wp1/`. **Must not edit shared compiler files or create
or remove persistent indexes.**

### Strategy matrix

For each route, compare these four shapes independently:

1. shared native traversal over multiple typed sibling routes;
2. independent native typed traversals;
3. shared explicit edge lookup with endpoint equality and multiple types; and
4. independent explicit edge lookups with endpoint and type equality.

Test the three expensive nested regions first, then the shared root region.
Replace only one region per candidate so its effect is attributable.

### Tasks

1. Obtain direction, edge collection, target collection, endpoint field,
   discriminator field, label, and target type from `resolveStorageRoute`.
2. Generate test-only candidate AQL by a structured helper or narrowly replace
   one identified physical region. Do not use resource-specific string
   replacement.
3. For INBOUND, compare edge `_to == parent._id`, the route's from-type
   discriminator, and the `_from` node join. For OUTBOUND, compare `_from`, the
   to-type discriminator, and `_to` node join.
4. Apply edge project/generation/auth scope before joining the node. Apply node
   project/generation/auth/resource-type scope before returning it.
5. Preserve current identity deduplication, ordering, child predicates, output
   shape, and compact retained fields.
6. Capture `EXPLAIN` before executing each candidate. Reject any explicit
   candidate that does not select the intended endpoint-first compound index.
7. Test equality and multi-type `IN` separately; do not infer that an index
   chosen for equality will be chosen for `IN`.
8. Cover root parent, nested parent set, zero/one/many neighbors, duplicate
   edges, restricted/unrestricted auth, null/named generation, and the proven
   outbound route.

### Required decision table

For each structural route class, report native/shared, native/independent,
explicit/shared, and explicit/independent median, scanned items, filtered
items, memory, index, and parity. Identify whether sharing becomes a loss once
typed equality enables the compound index.

### Stop conditions

- stop if a candidate needs a new persistent index;
- stop if scope must move after node materialization;
- reject a candidate that selects only the default edge index;
- reject resource-specific wins that cannot be expressed using route metadata;
- do not propose required-match lowering in this packet.

### Acceptance and handoff

Pass only if at least one route-generic explicit strategy satisfies the global
promotion gate. Hand off a typed `PhysicalTraversalStrategy` proposal, exact
filter ordering, supported route classes, unsupported cases, and profile
evidence. Do not implement it in production.

## WP2 - production endpoint-aware traversal lowering

**Purpose:** add the WP1-winning strategy without weakening native traversal
fallbacks.

**Owner:** coordinator. **Depends on:** WP1 pass. **Owns:** shared compiler
files, relevant focused tests, and `docs/benchmarks/round3/wp2/`.

### IR contract

Add a typed strategy to `PhysicalTraversal`; do not store AQL fragments. It
must describe:

- native graph versus explicit endpoint lookup;
- direction and endpoint field;
- edge-to-node join field;
- edge type discriminator field/value;
- route/collection binds already validated by `resolveStorageRoute`; and
- a reason/cost decision visible in diagnostics.

### Tasks

1. Extend physical validation to reject inconsistent endpoint/direction/join
   combinations and undefined collection binds.
2. Lower only the route classes proven by WP1. Required matches, variable-depth
   traversal, `ANY`, and unproven route shapes remain native.
3. Render edge scope before node lookup and node scope before child predicates.
4. Preserve duplicate-edge `UNIQUE`, deterministic order, compact projection,
   nested parent correlation, and outbound direction.
5. Add an independent rule switch for ablation. Default-enable it only if the
   generic protected corpus and GDC gate pass.
6. Report native versus endpoint decision, selected index expectation, and
   rejection reason in physical diagnostics.

### Tests

- plan validation for every legal/illegal direction combination;
- exact AQL shape and bind tests for inbound and outbound;
- duplicate edge, optional empty, child predicate, auth, and generation parity;
- live Explain asserts the endpoint-first index for enabled candidates;
- live result parity and five-run ablation profile.

### Acceptance

Meet WP1's measured gate in the production execution path. If only a structural
subset wins, use a deterministic structural gate; do not install a broad
heuristic based on FHIR type names.

## WP3 - traversal-time shaping projection experiment

**Purpose:** prove that selector values can be computed while the child node is
in scope, eliminating payload retention and the harmful second prepared array.

**Owner:** experiment worker. **May edit:** new
`internal/dataframe/*traversal_projection_experiment_test.go` and
`docs/benchmarks/round3/wp3/`. **Must not edit shared compiler files.**

### Candidate shape

One child-set item should retain only the union actually required downstream:

- `_id` only when a nested traversal consumes the node;
- `_key` or an explicit stable identity/order key when required;
- route/type fields only when a downstream operation needs them; and
- named selector arrays/scalars computed from the node payload in the traversal
  `RETURN` object.

It must not create a second prepared array, copy `payload`, or retain an
original-node escape hatch unless a specific unsupported consumer requires a
mixed fallback plan.

### Tasks

1. Walk all consumers of each physical set and collect selector requirements
   from aggregate values/predicates, pivot key/value, slice predicate/sort/
   projections, filters, derived expressions, and descendant navigation.
2. Canonicalize a projected selector by resource type, selector path,
   cardinality, fallback semantics, and evaluation scope. Never merge selectors
   that merely render similar text.
3. Classify retention as identity, navigation, ordering, projected selector,
   or unsupported payload fallback. Report the reason for every retained field.
4. Prototype the GDC child sets with one shaped object produced in the original
   set subquery. Rewire test-only consumers to read that same object.
5. Measure the candidate alone and combined with the WP1 winner, keeping the
   two improvements separately attributable.
6. Cover scalar and array selectors, repeated coding paths, null/missing
   intermediate objects, fallback chains, child filters, nested navigation,
   and mixed supported/unsupported consumers.

### Stop conditions

- reject any design that materializes both shaped and payload-bearing copies;
- do not silently disable a fallback selector;
- stop and propose an IR contract if consumer scope cannot be represented;
- do not count fewer AQL characters as a benefit.

### Acceptance and handoff

Pass if payload-retaining sets and repeated payload enumeration materially fall,
peak memory does not regress, exact parity holds, and the global performance
gate passes. Hand off a typed set-item projection contract, consumer rewiring
map, retention-reason schema, unsupported-case fallback, and evidence.

## WP4 - production traversal-time shaping

**Purpose:** replace prepared-set post-processing with the WP3-proven single
materialization shape.

**Owner:** coordinator. **Depends on:** WP3 pass. **Owns:** shared compiler
files, focused tests, and `docs/benchmarks/round3/wp4/`.

### Tasks

1. Add typed projected fields and retention requirements to the physical set
   item contract. Stable field names derive from canonical selector identity.
2. Build requirements after all consumers and descendants are known, then
   render them in the set's original `RETURN` object.
3. Rewire eligible extract, aggregate, pivot, slice, predicate, sort, and
   derived-field reads to projected fields.
4. Retain `_id` only for descendant traversal, identity only when dedup/order
   requires it, and payload only for an explicitly diagnosed unsupported
   consumer.
5. Remove or supersede `PhysicalPreparedSet` and its renderer when all supported
   uses migrate. Do not maintain two production representations.
6. Validate every projected reference is defined by its source set and cannot
   be read outside its item scope.
7. Add diagnostics listing projected selectors, retained fields/reasons,
   fallback consumers, estimated item width, and removed payload retention.

### Tests

- selector canonicalization and stable names;
- nested-navigation `_id` retention and leaf omission;
- aggregate/pivot/slice/filter/derived rewiring;
- fallback and mixed-consumer behavior;
- duplicate edge and ordering parity;
- live result/profile ablation with rule on/off.

### Acceptance

Meet the WP3 gate in production. Delete the rejected second prepared-array path
and its switches/tests once no production use remains. A rule that normally
retains payload is not accepted as traversal-time shaping.

## WP5 - leaf-set summary pushdown

**Purpose:** compute leaf aggregate, pivot, and representative-slice outputs
inside one child subquery instead of repeatedly scanning a materialized set.

**Owner:** coordinator. **Depends on:** WP4. **Owns:** shared compiler files,
focused tests, and `docs/benchmarks/round3/wp5/`.

### Eligibility

A set is initially eligible only when it has no navigated descendants and all
of its consumers can be represented by shaped selectors. A summary may contain
different consumers of the same source; byte-identical expression grouping is
not the eligibility rule.

### IR contract

Add a typed summary operation with:

- source identity/dedup contract;
- named aggregate outputs;
- named pivot outputs with key/value/collision semantics;
- named bounded slices with predicate, sort, tie-break, limit, and projections;
- explicit ordering requirements; and
- a fallback reason when a consumer remains outside the summary.

### Tasks

1. Group consumers by source set and compatible source identity/predicate scope,
   not by identical full expression text.
2. Deduplicate source identity once. Preserve aggregate-specific predicates and
   null filtering inside each named result.
3. Keep `COUNT` as direct set cardinality where possible. Preserve
   `COUNT_DISTINCT`, `DISTINCT_VALUES`, `MIN`, `MAX`, `FIRST`, and `EXISTS`
   semantics exactly.
4. Preserve pivot allowed columns, sparse keys, key/value array behavior,
   sorted collision selection, and grouped values.
5. Preserve slice filter, sort-before-limit, stable tie-break, and per-item
   projection. A bounded slice must not force an unbounded summary array.
6. Return one typed summary object and have the root projection read named
   fields without re-enumerating the child set.
7. Compare summary pushdown alone against WP4 and report incremental benefit.

### Tests

- each aggregate operation and null/empty behavior;
- count plus distinct values over one source;
- aggregate plus slice; aggregate plus pivot; all three together;
- same source with different predicates remains semantically independent;
- pivot collision/sparse columns; slice ties/limits; duplicate edges;
- high fan-out and empty leaf sets; auth/generation parity;
- live profile proves fewer post-materialization loops.

### Acceptance

Require at least 5% incremental whole-query improvement over WP4 and at least
15% combined improvement over WP0, with no memory regression. Otherwise reject
the operation and remove its unused IR instead of calling diagnostics fusion.

## WP6 - identity, ordering, and deduplication proof

**Purpose:** identify exactly which `UNIQUE` and `SORT` operations are redundant
without guessing about AQL executor order.

**Owner:** experiment worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round3/wp6/`. **Must not edit shared compiler files.**

### Tasks

1. Inventory every current `UNIQUE` and `SORT`, its source set, consumer, key,
   tie-break, and semantic reason.
2. Model candidate properties on paper/test helpers:
   - identity unique by node `_id` or `_key`;
   - stable ascending order by an exact key tuple;
   - unordered;
   - bounded by a known limit; and
   - property invalidation by filter, projection, union, traversal, or grouping.
3. Prove duplicate-edge behavior when replacing object-level `UNIQUE` with
   identity-key deduplication. Payload/object equality is not identity equality.
4. Test whether set materialization order can satisfy a slice's exact sort and
   tie-break. Do not assume traversal order or `UNIQUE` output order.
5. Detect and remove duplicated sort keys such as `_key, _key` only as a
   correctness cleanup; measure separately from executor-level sort removal.
6. Prototype removal of one operation at a time and capture parity/profile
   evidence. Include high fan-out and intentionally duplicated edges.

### Acceptance and handoff

Pass only individual property rules that remove executor sort/dedup work and
meet the targeted/whole-query gate. Hand off transfer/invalidation rules and a
list of proven removals. Reject any rule dependent on undocumented traversal
order.

## WP7 - production physical properties

**Purpose:** encode only WP6-proven identity/order facts and use them to avoid
redundant work.

**Owner:** coordinator. **Depends on:** WP6 pass. **Owns:** shared compiler
files, focused tests, and `docs/benchmarks/round3/wp7/`.

### Tasks

1. Add explicit physical properties for identity uniqueness, ordered key tuple,
   and bound. Unknown is the safe default.
2. Define property transfer and invalidation for every physical operation.
3. Make dedup and sort requirements explicit consumers of those properties;
   suppress an operation only when the input proves the exact requirement.
4. Normalize duplicate sort keys without changing requested direction or null
   behavior.
5. Emit diagnostics for every retained and removed sort/dedup operation with
   its proof.
6. Add rule-level ablation and profile independently from WP2/WP4/WP5.

### Acceptance

All WP6 semantic cases pass, diagnostics contain a proof for every removal, and
the production profile reproduces the measured benefit. An unknown property
must render the current conservative operation.

## WP8 - conditional batch-root execution experiment

**Purpose:** test whether processing the root window as a set is materially
faster than executing correlated child work once per root.

**Run only if:** the five-run warm median remains above 3 seconds after all
accepted WP2/WP4/WP5/WP7 changes.

**Owner:** experiment worker. **May edit:** isolated experiment tests and
`docs/benchmarks/round3/wp8/`. No shared production edits.

### Candidate shape

1. Materialize the scoped, sorted, limited root window exactly once.
2. Look up relevant scoped edges for the complete root identity set.
3. Join scoped child nodes, preserving root identity on every row.
4. Group/deduplicate/shape by root identity.
5. Reassemble output in original root-window order, including roots with no
   optional children.

### Tasks and risks

- compare correlated and batched shapes at limits 25, 100, and 1,000;
- profile edge-index behavior for `IN root_ids` versus per-root equality;
- prove auth and generation scope before grouping;
- prove duplicate-edge and optional-empty behavior;
- bound intermediate cardinality and memory; and
- test one-hop and nested paths separately before composing them.

Reject batching if `IN` loses the compound index, memory grows above the global
gate, optional roots disappear, or improvement exists only at one hard-coded
limit/resource.

### Acceptance and handoff

Require at least 15% additional whole-query improvement at limit 1,000 and no
more than 5% regression at 25/100. Hand off a typed root-batch/subplan proposal
and evidence; do not productionize in this package.

## WP9 - conditional catalog-backed physical costing

**Purpose:** choose among proven physical strategies using real generic
cardinality/width evidence.

**Run only if:** at least two production-safe strategies have shape-dependent
winners. Statistics are not useful when there is no choice to make.

**Owner:** coordinator or isolated investigator followed by coordinator merge.

### Tasks

1. Trace current `catalog.PopulatedReference.EdgeCount` production ownership and
   define a read-only compilation statistics snapshot keyed by validated route,
   project, generation, and authorization visibility where available.
2. Never query discovery repeatedly during rendering. Fetch/cache statistics at
   the request/compiler boundary with an explicit freshness policy.
3. Estimate root count, edge fan-out, selectivity, retained item width, and
   expected materialized rows. Record unknown separately from zero.
4. Use statistics only to choose among strategies already proven semantically
   equivalent: shared/independent, native/endpoint, shaped/fallback, or
   correlated/batched.
5. Provide a deterministic no-statistics fallback matching a proven production
   policy. Statistics never change result semantics.
6. Emit the inputs, estimate, selected strategy, and reason in diagnostics.
7. Test missing, stale, zero, extreme, and contradictory statistics and verify
   they cannot cause invalid IR.

### Acceptance

The cost policy must select the faster strategy across a route/cardinality
corpus with no protected-case regression. Merely plumbing `EdgeCount` without a
measured strategy-selection win is not completion.

## WP10 - final integration, deletion, and report

**Purpose:** retain only proven generic translations and leave one coherent
production compiler.

**Owner:** coordinator only. **Depends on:** every accepted package.

### Tasks

1. Rebase decisions on a fresh WP0 same-session baseline.
2. Run each accepted rule independently and cumulatively at limits 25, 100, and
   1,000. Detect interactions where two individually useful rewrites regress in
   combination.
3. Run the full generic unit suite, physical renderer/validator suite, result
   parity suite, live Arango Explain/profile suite, auth/generation cases, and
   proven outbound route.
4. Save final raw AQL/profile artifacts and a comparison table against WP0.
5. Delete rejected experiments, dead switches, stale diagnostics, unused IR,
   the superseded prepared-array path, and documentation claims contradicted by
   production code.
6. Confirm no FHIR resource, route, example set variable, or example column is
   hard-coded in production optimizer logic.
7. Run `go test ./... -count=1` and the exact five-run demo/profile commands.

### Final acceptance target

- exact result parity and zero full scans;
- endpoint-first index selected wherever the accepted strategy requires it;
- peak memory below 200 MB;
- five-run warm Arango median below 4.5 seconds after endpoint/projection work;
- stretch median below 3 seconds after summary/property or batch work; and
- a plain explanation of which physical rewrite removed which scanned items,
  payload materialization, set loop, dedup, or sort.

If the target is not met, report the remaining top profile region honestly. Do
not label added compiler machinery as an optimization without disappeared
runtime work.

## Luna work-package prompt template

Use this prompt for each worker, replacing the placeholders exactly:

```text
Execute <WP number and title> in Loom.

Read docs/LUNA_AQL_EXECUTION_ROUND_3.md and
docs/AQL_EXECUTION_IMPLEMENTATION_AUDIT.md completely, then read every source
file named by the package. Own only <exact paths>. Do not edit shared compiler
files unless this package designates you as coordinator.

Before editing, record git status and SHA-256 hashes for every owned existing
file. Preserve unrelated dirty changes. Use fhirschema and
resolveStorageRoute; never hard-code a FHIR resource, edge, child_set variable,
or GDC column. Preserve every global semantic contract.

Run the package baseline first. Implement only this package. Run its named
unit/live tests and produce its required evidence directory. Report changed
files, before/after hashes, raw profile metrics, rejected experiments, the
enable/cost-gate/reject decision, and coordinator decisions required.

Stop rather than guess if an unowned IR change is required, an owned file
changes concurrently, baseline hashes/results differ unexpectedly, scope or
ordering semantics would change, the intended index is not selected, or the
required profile benefit is absent.
```
