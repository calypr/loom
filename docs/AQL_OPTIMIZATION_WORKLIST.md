# AQL optimization work list

This is the post-measurement work list for Loom's physical dataframe
compiler. It is deliberately compiler-first: frontend work must not hide an
unbounded or repeatedly correlated AQL plan.

It applies to every schema-defined FHIR relationship. Nothing below may
special-case Patient, GDC, `subject_Patient`, or a fixture resource type.
`fhirschema` remains the source of valid routes and selectors; the physical
optimizer decides whether a particular request can share work. The GDC case
matrix is an integration specimen that exercises fields, filters, aggregates,
pivots, slices, and nested traversal together; it is not the shape the
optimizer is allowed to recognize or optimize specially.

## Measured starting point

The checked-in GDC case-matrix request, run against the loaded `META` data,
has these measured characteristics:

| Measure | Observation |
|---|---:|
| 1,000 rich Patient rows | about 8.15 seconds/request |
| Arango cursor time | about 8.10 seconds/request |
| compilation | about 1 millisecond/request |
| Loom row shaping | about 2 milliseconds/request |
| GraphQL serialization + HTTP | about 54 milliseconds/request |
| physical traversal sets | 6 |
| traversals shared today | 0 |
| broad sharing opportunities | 1 group / 3 sibling sets |
| repeated rich-set consumers | 4 sets |

The broad sharing opportunity is the generic pattern “one parent variable,
one directed edge label, several target resource types.” In the current GDC
request it appears as Patient's `subject_Patient` traversals to Condition,
Specimen, and Observation. The current optimizer must not share it because
the full scoped subplans are not yet proven alpha-equivalent after variable
rebinding.

Repeated rich consumers are also real, but distinct from repeated traversals:
Condition and Specimen each have two aggregates plus a slice; DocumentReference
has three aggregates plus a slice; Observation has two aggregates plus a
pivot. The relationship set is materialized once, then the current renderer
loops over it independently for each rich expression.

## Non-negotiable contracts

Every work package must preserve:

1. result parity: row count, root order, columns, null/empty behavior, array
   ordering, pivot collision behavior, and representative slice selection;
2. authorization and dataset-generation isolation on both traversed edge and
   target node;
3. optional versus required traversal semantics;
4. bind-only user values and schema-validated selector paths;
5. generic operation over every valid schema route.

Do not claim an optimization from rendered-AQL string similarity. Prove it
with physical-plan tests, old-versus-new result parity on Arango, and a live
`PROFILE` comparison of the same request.

## Required benchmark and parity corpus

Every optimization must run against a corpus of semantic shapes, not one
dataframe. Maintain these generic cases using routes discovered from
`fhirschema` and data loaded from `META` where available:

| Case | What it protects |
|---|---|
| scalar root preview | root scope, sort, limit, and projection cost |
| one optional child | ordinary field selection and empty-child behavior |
| sibling targets on one edge label | generic traversal-prefix sharing |
| proven outbound route | direction-specific edge discriminator behavior |
| required relationship match | semi-join/root filtering semantics |
| child and nested filters | predicate placement and auth/generation scope |
| deep traversal (3+ hops) | captures, variable rebinding, and nested scope |
| aggregate-only child | count/distinct/exists value semantics |
| aggregate + slice over one set | prepared-set reuse without ordering drift |
| Observation-style pivot | grouped/keyed values and sparse columns |
| mixed rich dataframe | cross-feature composition; the GDC case matrix lives here |
| generated root sweep | every root resource type advertised by `fhirschema` compiles or rejects deterministically |

Fixture names may describe their contents, but optimizer implementation and
tests must assert only physical properties: route equality, scope equality,
typed selector semantics, and output parity. A new route with the same
physical properties must receive the same rewrite without code changes.

## WP0 — Make live Arango PROFILE the source of truth

**Goal:** attribute the 8-second request to actual Arango executor nodes
before changing the renderer.

### Implement

1. Extend `internal/store/arango` with a profile operation for a parameterized
   query. It must accept the generated AQL and bind variables without
   interpolation.
2. Capture plan nodes, calls, items, filtered items, runtime, and estimated
   cost wherever the installed Arango version provides them. Preserve warnings,
   indexes, and applied optimizer rules already collected by `EXPLAIN`.
3. Add an opt-in dataframe diagnostic path; never run `PROFILE` on normal
   frontend requests. A local-only GraphQL flag or a dedicated developer CLI
   mode is acceptable.
4. Emit a stable, compact report grouped by node type and by AQL source
   fragment: root scan, each traversal set, aggregate, pivot, slice, sort,
   and return.
5. Save sanitized JSON profile artifacts for a small/medium/large sample of
   the required corpus—not only GDC—under `docs/benchmarks/` or a gitignored
   local artifact path.

### Tests and gate

- Unit-test profile JSON decoding against checked-in fixtures.
- Live tests: profile representative root, sibling, deep, required-match, and
  pivot cases; assert the root scoped index and `fhir_edge` edge index are
  selected with no full collection scan.
- Record the top five runtime nodes. Do not start WP1 until the profile output
  identifies whether root sibling traversals dominate as expected.

## WP1 — Canonicalize generic traversal prefixes

**Goal:** represent the shareable prefix of any physical traversal without
depending on incidental variable names or semantic provenance.

### Implement

1. Add a typed `PhysicalTraversalPrefix` (or equivalent canonical helper)
   containing source variable, direction, edge collection, edge label, edge
   target-type discriminator, project/generation/auth constraints, and the
   canonical node/edge variables used inside that prefix.
2. Split each child `PhysicalSet` into:
   - a shareable traversal-and-scope prefix;
   - a target-type subset;
   - consumer-specific filters and nested work.
3. Implement alpha-renaming for target and edge variables across typed filters,
   auth checks, selector extractions, required-match predicates, nested sets,
   aggregate predicates, pivots, and slices.
4. Make the share key derive only from semantic physical behavior. It must not
   include alias names, projection names, or `PhysicalSource` provenance.
5. Keep an explicit `not shareable` reason for each rejected set: different
   direction, label, parent variable, scope, required/optional mode, or a
   not-yet-supported operation.

### Tests and gate

- Table tests for alpha-equivalent prefixes with different generated variable
  names.
- Negative tests for every rejection reason.
- Validate the rewritten plan before rendering; no renderer-only rewrite.
- At least one corpus sibling-route fixture must move from zero scope-safe
  candidates to a proven candidate group, and a structurally equivalent route
  with different FHIR resource types must share without a code change.

## WP2 — Render shared neighbor traversals and typed subsets

**Depends on:** WP1.

**Goal:** execute one generic neighbor traversal for compatible siblings, then
derive each resource-type-specific child set from it.

### Implement

1. Add a physical operation for a shared neighbor set with a bind-backed list
   of target types.
2. Render one correlated AQL subquery per parent row:

   ```aql
   LET shared_neighbors = (
     FOR node, edge IN 1..1 INBOUND parent fhir_edge
       FILTER scoped_edge_and_node_predicates
       FILTER edge.label == @label
       FILTER node.resourceType IN @target_types
       RETURN node
   )
   ```

3. Render each child as a typed subset of `shared_neighbors`; retain its own
   target-type check and its consumer-specific predicates.
4. Preserve child ordering (`_key` or the existing explicit semantic order),
   uniqueness, and empty-set behavior.
5. Support both INBOUND and proven OUTBOUND routes. Do not enable `ANY` until
   a separate parity proof exists.
6. Keep required relationship matching as a semi-join. It may consume a shared
   prefix only after proving that it does not accidentally materialize optional
   output or change root filtering order.

### Tests and gate

- Result parity between unshared and shared plans for optional siblings,
  filtered siblings, auth-restricted paths, empty sets, and nested children.
- Live parity for the sibling-route fixture, a deep traversal fixture, and the
  mixed GDC matrix at 25 and 1,000 rows where the dataset contains that many
  roots.
- `PROFILE` must show fewer traversal executor calls/items for the shared
  sibling group and lower or equal total Arango runtime. If it does not, keep
  the rewrite behind a cost gate rather than forcing it globally.

## WP3 — Fuse rich consumers over one child set

**Depends on:** WP0; can begin design in parallel with WP1.

**Goal:** avoid re-scanning and re-extracting FHIR selectors independently for
every aggregate, pivot, and slice over the same child set.

### Implement

1. Add a typed `PhysicalPreparedSet` or `PhysicalSetProjection` operation,
   owned by a child relationship set.
2. Collect the union of selector inputs required by that set's aggregates,
   pivot key/value expressions, slice predicates/sort keys, and slice fields.
3. Render one bounded pass over the child set that produces a small prepared
   object per child. It may contain the original node only when nested work
   still requires it; otherwise project just the selected values.
4. Lower aggregate, pivot, and slice expressions to consume prepared values.
   Preserve each feature's own null, distinct, predicate, collision, and sort
   semantics.
5. Do not prepare selectors that are used once; require at least two consumers
   or a profile-backed benefit.
6. Do not force unbounded arrays into memory. Keep representative slices
   bounded, and retain aggregate streaming/grouping where Arango can do it.

### Tests and gate

- Unit parity for `COUNT`, `COUNT_DISTINCT`, `EXISTS`, `DISTINCT_VALUES`,
  pivot collisions, null selectors, and sorted slices.
- Fixture assertions for aggregate/slice, aggregate/pivot, and nested-child
  reuse sites across more than one resource family.
- `PROFILE` must show fewer selector/subquery executions and no increase in
  returned intermediate items that erases the gain.

## WP4 — Fuse compatible aggregates and pivots before prepared-set lowering

**Depends on:** WP3 design; implementation may be folded into WP3 if simpler.

**Goal:** exploit operations that can share more than selector extraction.

### Implement

1. Group aggregates with the same source and predicate into one AQL pass.
2. Group pivots with the same source/key/value/predicate and differing allowed
   columns into one grouped map, then project requested column subsets.
3. Share predicate evaluation between aggregates and slices only when the
   predicate is identical and preserving it does not affect slice ordering.
4. Maintain a deterministic operation ordering and stable bind-variable names
   so Explain fixtures remain readable.

### Tests and gate

- Same-source / different-predicate operations remain separate.
- Same-source / same-predicate groups have byte-for-byte output parity.
- Observation pivot receives a dedicated live `PROFILE` gate because `COLLECT`
  can be slower than separate bounded loops on sparse data.

## WP5 — Predicate and required-match reuse

**Depends on:** WP1 and WP2.

**Goal:** eliminate duplicate graph work when the same relationship appears in
both a required root filter and an optional projected child set.

### Implement

1. Teach the physical plan to identify an `EXISTS`/required-match predicate
   whose route and scope prefix match a materialized child set.
2. Retain root filtering before root `SORT`/`LIMIT`; do not turn a required
   predicate into post-limit filtering.
3. Permit a shared root-scoped existence result only when it cannot leak child
   data across authorization scopes.
4. Record reuse versus rejection in compiler-plan diagnostics.

### Tests and gate

- Required match plus optional projection parity for no match, one match, many
  matches, restricted auth, and nested match routes.
- Live explain/profile demonstrates fewer duplicate edge traversals.

## WP6 — Index and query-shape audit driven by PROFILE

**Depends on:** WP0; repeat after WP2 and WP3.

**Goal:** ensure the optimized AQL can use Arango's physical indexes rather
than merely containing the right filters.

### Implement

1. Inventory the actual root indexes used for project, generation, auth path,
   `_key` sort, and root resource collection selection.
2. Inventory `fhir_edge` indexes used by direction. Confirm whether label and
   type discriminators are post-filtered after the edge index and whether a
   persistent composite index would materially reduce traversal fan-out.
3. Test candidate indexes against the loaded META data and a larger synthetic
   fan-out fixture. Do not add every possible composite index by default;
   index write cost matters for bulk ingest.
4. Compare root-first `LIMIT` placement, filter movement, and traversal
   options only with result-parity proof.

### Tests and gate

- Explain assertions cover index name, fields, absence of collection scans,
  and estimated item/cost regressions.
- Profile evidence justifies each new index and documents its ingest tradeoff.

## WP7 — Cost model, diagnostics, and regression gates

**Depends on:** WP0 through WP6 incrementally.

**Goal:** make optimizer decisions observable and prevent a future renderer
change from silently restoring correlated work.

### Implement

1. Keep GraphQL diagnostics opt-in or development-only; production dataframe
   responses should not expose compiler internals by default.
2. Report: traversal-set count, shared set count, scope-safe candidate count,
   broader opportunity count, rejected reasons, and rich-source consumer
   counts.
3. Add a compiler cost heuristic that only enables sharing/fusion when its
   estimated savings exceeds extra materialization cost. Start conservative;
   correctness and profile evidence outrank cleverness.
4. Add benchmark commands for 25, 100, and 1,000 rows with one cold run and
   at least three warm runs. Print per-request—not cumulative—timings.
5. Track response bytes, returned rows, compile time, Arango execution,
   serialization, profile node totals, and plan diagnostics together.

### Acceptance gate

The entire corpus must have committed baseline and post-change evidence. GDC
is one required mixed-feature case, not a privileged one. A performance claim
requires all of:

- unchanged output result hash/canonical comparison;
- no authorization or generation-scope regression;
- no full collection scan or lost required index;
- lower profile work for the targeted node(s);
- lower or statistically neutral warm 1,000-row runtime.

## Execution order and parallelism

| Order | Package | Parallelism | Why |
|---:|---|---|---|
| 1 | WP0 profiling | alone | establishes the real target before rewrites |
| 2 | WP1 canonical prefix | alone | changes the physical IR contract |
| 3 | WP2 shared traversal | after WP1 | first high-value generic rewrite |
| 3 | WP3 prepared sets | design only alongside WP2 | implementation waits for stable IR |
| 4 | WP3 implementation | after WP2 profile | avoids optimizing the wrong cost |
| 5 | WP4 and WP5 | separate workers | distinct physical expression versus predicate ownership |
| 6 | WP6 | after each major rewrite | index choices require final query shapes |
| 7 | WP7 | continuous | gates every merge rather than a final cleanup |

The first implementation target is therefore not “make the GDC query fast.”
It is “make every schema-defined sibling traversal safely shareable when the
typed physical plan proves it is equivalent,” then use `PROFILE` to verify the
benefit across the corpus, including—but never limited to—GDC.

## Luna multi-agent execution map

This work can use several Luna agents, but only one agent at a time may own a
physical IR or renderer contract. The safe unit of parallelism is a package
with a narrow output contract, not a broad instruction such as “optimize AQL.”

### Coordinator responsibilities

The coordinator owns these files and resolves all interface changes before
implementation workers begin:

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_execution.go`
- `internal/dataframe/physical_diagnostics.go`

The coordinator must publish one short interface note before the first merge:

1. operation/type names and fields for traversal prefixes, shared sets, typed
   subsets, and prepared sets;
2. ownership and lifetime of physical variables/captures;
3. which phase lowers, rewrites, validates, and renders each operation;
4. whether a new operation is enabled immediately or rendered behind an
   explicit optimizer rule;
5. the canonical result-parity comparator and profile artifact format.

No worker may rename or reshape those types independently. If a worker needs a
new field, it proposes the exact change and waits for the coordinator to add
it or approve it.

### Wave A — independent foundations

These packages may begin together because they do not change the physical plan
contract.

| Worker | Scope and owned files | Deliverable | Merge gate |
|---|---|---|---|
| A1: Profile adapter | `internal/store/arango/`, new profile fixtures/tests | Parameterized `PROFILE` request/decoder, stable node summaries, warnings/index/rule capture | Unit fixtures plus a live opt-in profile test; no dataframe renderer edits |
| A2: Benchmark corpus | `conformance/compiler/fixtures/`, `examples/`, benchmark docs/tests | Route-neutral corpus described above, fixture loader helpers, canonical output comparator | Existing GDC remains; each fixture compiles or has deterministic rejection |
| A3: Diagnostics/CLI | `cmd/dataframe-query/`, GraphQL diagnostic mapping, docs | Opt-in display of plan facts, timing, profile artifact path; 25/100/1K benchmark commands | No optimizer behavior changes; works against server without diagnostics where practical |
| A4: Index audit | `internal/store/arango/` Explain helpers, integration tests, docs | Inventory of root/edge indexes and Explain/profile assertions for corpus shapes | No new persistent index without recorded profile evidence and ingest-cost note |

**A-wave handoff:** A1 publishes `ProfileReport`; A2 publishes fixture IDs and
expected shape metadata; A3 consumes both as read-only contracts; A4 consumes
A1 reports. These workers should avoid editing the same generated GraphQL files
by having A3 own schema/model regeneration.

### Wave B — traversal-sharing vertical slice

These are sequential at the IR boundary but can overlap in test preparation.

| Worker | Scope | Depends on | Deliverable |
|---|---|---|---|
| B1: Prefix IR | typed traversal-prefix decomposition and validator | coordinator interface note | canonical prefix and rejection-reason API; no changed runtime behavior yet |
| B2: Prefix tests | physical-plan fixture builders and alpha-renaming tests | B1 type signature | positive/negative equivalence corpus, auth/generation/required-match cases |
| B3: Shared renderer | optimizer rewrite plus renderer support | B1 merged; B2 tests available | one shared neighbor traversal plus typed subsets, initially behind a rule |
| B4: Sharing parity/profile | live Arango parity and profile comparisons | B3 | proof that the generic rewrite reduces traversal work for at least two distinct route families |

Only B1 edits the initial prefix type. Only B3 edits sharing rewrite/rendering.
B2 and B4 may prepare code in parallel but must rebase on B1/B3 respectively.

### Wave C — rich-expression reuse vertical slice

This wave may begin its semantic/test work while Wave B is in progress, but it
must not merge physical-plan type changes until B1's prefix contract is fixed.

| Worker | Scope | Depends on | Deliverable |
|---|---|---|---|
| C1: Prepared-set design | proposal plus tests for selector union and value lifetime | coordinator interface note | approved prepared-set IR contract and decision table for one versus many consumers |
| C2: Aggregate fusion | aggregate grouping tests and lowering helpers | C1 contract | same-source/same-predicate aggregate grouping with parity proof |
| C3: Pivot/slice reuse | pivot/slice test corpus and selector-extraction helpers | C1 contract | prepared-value consumers preserving collision, null, sort, and limit behavior |
| C4: Rich profile gate | profile comparison harness | A1 and C2/C3 | before/after node-level evidence for aggregate, pivot, and slice shapes |

C2 and C3 have separate feature ownership. Neither may modify the other's
renderer branch; the coordinator integrates their operations after the
prepared-set contract is merged.

### Wave D — cross-cutting reuse and hardening

| Worker | Scope | Depends on | Deliverable |
|---|---|---|---|
| D1: Required-match reuse | physical required-match lowering and parity tests | B3 | shared-prefix reuse without changing pre-limit semi-join semantics |
| D2: Cost policy | optimizer decision heuristic and explainable rejection reasons | B3, C4 | conservative enablement rule, diagnostics, and disable switch |
| D3: Index changes | candidate index migrations/bootstrap plus measurement | A4, B4, C4 | only profile-justified index additions and documented ingest tradeoff |
| D4: Regression suite | corpus benchmark automation and result/profile artifact checks | A1–A4, B4, C4 | merge-blocking performance/parity gate |

### Required worker prompt template

Give each Luna worker a bounded prompt in this form:

```text
You own <exact files/package>. Do not edit <coordinator-owned files>.

Objective:
<one concrete operation or harness>

Read first:
- docs/AQL_OPTIMIZATION_WORKLIST.md
- <specific existing implementation/tests>

Contract:
- Inputs: <types/fixtures>
- Outputs: <types/report/tests>
- Must not: special-case FHIR resource names or change public dataframe results.

Acceptance:
1. <named unit tests>
2. <named live Arango parity/Profile test if applicable>
3. go test <packages>
4. report changed files, interface assumptions, and any coordinator decision needed.
```

### Merge discipline

1. Merge Wave A first, but permit A1/A2/A3/A4 independent commits.
2. Merge B1 before any shared traversal renderer code.
3. Merge B2 test coverage before B3, then require B4 evidence before enabling
   sharing by default.
4. Merge C1 contract before C2/C3 renderer work; merge C4 evidence before
   enabling fusion by default.
5. Run the complete corpus after every coordinator merge, not only a worker's
   narrow package tests.
6. If a physical IR conflict appears, stop the conflicting workers and have
   the coordinator choose one contract; do not resolve it by retaining two
   transitional representations.

### Recommended initial allocation

With four Luna workers plus a coordinator:

- Worker 1: A1 profile adapter
- Worker 2: A2 generic fixture/benchmark corpus
- Worker 3: A3 diagnostics/CLI and GraphQL regeneration
- Worker 4: B2 traversal-sharing parity and alpha-renaming test design
- Coordinator: freeze B1 prefix IR after reviewing A2/B2 requirements, then
  assign B1 and B3 sequentially.

With eight workers, add A4, C1, C2 test preparation, and C3 test preparation.
Do not assign two workers to `physical_optimize.go` or `physical_render.go` at
the same time.

## Execution status

As of the current execution pass:

- **Complete:** WP0 profile adapter and corpus profile fixtures.
- **Complete:** generic FHIR benchmark/parity corpus beyond GDC.
- **Complete:** compiler timing and plan diagnostics CLI path.
- **Complete:** B1 canonical traversal-prefix decomposition and rejection
  reasons.
- **Complete:** B3 generic scope-safe sibling traversal sharing, including
  recursive variable rebinding and typed subsets. It remains constrained to
  decomposition-proven plans.
- **Complete:** WP3b recursive nested object rendering and recursive object
  cycle validation.
- **Experimental and disabled:** WP3 prepared-set reuse has a physical operation
  and renderer, but the live GDC profile was slower and used substantially more
  memory. It is not part of the production-default optimization policy.
- **Diagnostics only:** WP4 can classify byte-identical rich consumers, but no
  fusion lowering or renderer path currently executes fused aggregate, pivot,
  or slice consumers. The GDC profile also produced no eligible multi-consumer
  groups under that classifier.
- **Complete:** WP6 profile-driven Explain/index corpus harness (live execution
  remains opt-in with `LOOM_COMPILER_ARANGO_INTEGRATION=1`).
- **Complete:** WP5 required-match reuse: duplicate required EXISTS routes are
  deduplicated only when their typed route/filter key is identical, with reuse
  counted separately from optional traversal sharing. Required routes can also
  materialize selected child fields after the pre-window semi-join.
- **Complete:** WP7 structural corpus regression gates, opt-in compiled-query
  profiling API, generic Explain/profile artifacts, and the conservative
  structural cost policy (`LOOM_PHYSICAL_COST_POLICY` / minimum-savings switch).
- **Complete:** WP8 compatibility renderer deletion and legacy test migration.
  The production physical compiler is now the only dataframe compiler path;
  the old lowered renderer, planner, named-set types, and compatibility-only
  tests have been removed.

The complete repository test suite is green after the completed packages.
Production and conformance code now use the physical compiler exclusively.

## Explicit renderer completion packages

The work packages above also require these two explicitly named renderer
packages. They were previously implicit and should be assigned as separate
Luna tasks.

### WP3b — Nested object expression rendering

**Depends on:** WP1 and the frozen physical expression contract.

`PhysicalObjectExpression` exists in the IR and validator, but it must have a
real renderer before the compatibility renderer can be removed.

Implementation:

1. Add recursive `renderObject` support to `physical_render.go`.
2. Render every object field through `renderExpression`, including nested
   objects, extracts, aggregates, pivots, slices, and optional child values.
3. Use bind-backed output names; never concatenate user-provided field names
   into AQL source.
4. Preserve null/empty behavior and deterministic field ordering.
5. Support objects inside representative slices and nested traversal
   projections.
6. Reject recursive physical object cycles during plan validation.

Acceptance requires scalar-object, nested-object, object-with-pivot, and
object-with-slice fixtures plus live result parity. No object expression may
pass validation without a renderer test.

### WP8 — Compatibility renderer deletion

**Depends on:** WP2, WP3, WP3b, WP4, WP5, and the complete parity corpus.

This is a gated package, not incidental cleanup:

1. Prove repository-wide that `CompileRequest` is the only production
   dataframe execution entrypoint.
2. Replace parity tests that depend only on compatibility AQL text with
   semantic-result and live Arango parity tests.
3. Delete `compileLowered`, `Lower`, `lowerSemanticBuilder`, `NamedSet`, and
   compatibility-only helpers/tests in dependency order.
4. Remove stale compatibility metadata while preserving any still-public API
   fields.
5. Run the full corpus, GraphQL suite, Explain/profile suite, and generated-root
   sweep after deletion.

The deletion gate fails if any physical expression validates without rendering,
any production call site bypasses `CompileRequest`, or any parity fixture still
depends on compatibility-only output text.
