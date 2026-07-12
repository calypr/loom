# Physical Renderer Replacement Plan

## Objective

Replace the compatibility `compileLowered(Builder, limit)` AQL string compiler
with one executable pipeline:

```text
GraphQL Builder
  -> SemanticPlan
  -> PhysicalPlan
  -> RenderPhysicalPlan
  -> parameterized AQL
```

The physical plan is the only place where AQL execution strategy is chosen.
It must preserve the semantic plan exactly, including root row grain, selected
columns, filters, required relationship matches, pivots, aggregates,
representative slices, authorization scope, and dataset generation scope.

`compileLowered` remains only as a test oracle during the migration. Do not
move its string-building helpers into the new renderer.

## Non-negotiable invariants

- No raw user-controlled AQL fragments enter the physical plan or renderer.
- Every root, traversed node, and edge is constrained by project, dataset
  generation, and authorization scope.
- A physical plan validates lexical variable scope and bind-variable ownership
  before rendering.
- The root execution window (`SORT root._key`, then optional `LIMIT`) occurs
  before optional traversal work, unless a semantic ordering contract says
  otherwise.
- Output column names and array/scalar/null semantics match the current
  compiler exactly.
- A new route is enabled only after deterministic result parity and live
  Arango `EXPLAIN` coverage prove it is correct and index-backed.

## Target physical IR

Keep the existing root scan, traversal, filter, derived-let, sort, limit, and
return operations, but replace the current navigation-only subset with typed
operations that can describe every semantic feature.

| Capability | Required physical operation/value |
| --- | --- |
| FHIR selector extraction | `Extract` value: root value, selector path, fallback paths, value mode |
| Typed filters | `Predicate` tree: all/any/not, comparison, exists, membership, string match; typed bind values only |
| Relationship existence | `Exists` expression with a bounded typed traversal subplan and `LIMIT 1` |
| Optional child traversal | `Set`/subquery operation with traversal, resource-type filter, predicate, uniqueness, and stable sort |
| Shared traversal prefixes | `Set` operation can feed typed filtered subsets; lowering records shared-source provenance |
| Aggregates | `Aggregate` expression: count, distinct count, values, first/representative, and predicate guard |
| Pivots | `PivotMap` expression: key selector, value selector, permitted columns, collision/value mode |
| Representative slices | `Slice` expression: typed source set, predicate, stable sort, limit, nested projection |
| Derived fields | `Derived` expression: count, values, first, pivot map, and references to prior typed sets only |
| Projection | projection values can be scalar, array, object, aggregate, pivot, slice, or derived expression |

Do not add one physical operation per historical AQL helper. Prefer a compact,
validated expression tree plus explicit set-producing operations. The renderer
owns only serialization of that tree; semantic interpretation belongs in the
lowerer.

## Work packages

### P1 — Freeze the executable IR contract

**Owner:** compiler core. **Blocks:** all renderer work.

1. Introduce `PhysicalExpression`, `PhysicalSet`, and typed predicate nodes in
   `internal/dataframe/physical_plan.go`; make operation payloads a closed,
   validated union.
2. Add explicit value cardinality and null behavior to expressions. Do not
   infer scalar-versus-array behavior from renderer context.
3. Add `PhysicalSubplan`/set scope rules so a subquery cannot reference a
   variable introduced later or outside its parent scope.
4. Version the physical plan and provide deterministic JSON fixtures for it.
5. Extend `PhysicalPlan.Validate` with bind type, selector-path, collection
   bind, provenance, and mandatory-scope checks.

**Acceptance:** invalid plans fail before AQL rendering; a fixture can express
every current semantic selection without a raw AQL string.

### P2 — Direct semantic-to-physical lowering

**Owner:** compiler core. **Blocks:** P3–P7.

1. Add `BuildPhysicalPlan(SemanticPlan, ExecutionOptions) (PhysicalPlan,
   error)`.
2. Stop using `lowerSemanticBuilder` in this new route. It may remain only for
   compatibility-oracle tests until P9.
3. Lower root scan, root project/generation/auth scope, deterministic root
   window, and `_key` projection directly from the semantic plan.
4. Lower each semantic node using the proven `storage_route` contract;
   unsupported outbound/ANY routes must remain explicit compiler errors.
5. Carry semantic aliases and source fields into `PhysicalSource` for errors,
   explain output, and goldens.

**Acceptance:** navigation-only `CompileRequest` uses this route with no
`Builder.Sets` dependency and remains byte-stable where practical.

### P3 — Typed selector extraction and root filters

**Can run after P1/P2.**

1. Lower `SemanticField` selectors, fallbacks, and value modes to `Extract`.
2. Lower `TypedFilter` to physical predicate trees; encode literals in bind
   vars and retain existing FHIR payload traversal semantics.
3. Render extraction/predicate trees as fixed AQL templates, including nested
   array iteration and null behavior.
4. Add parity fixtures for each filter operator and every supported value mode.

**Acceptance:** root-only field/filter requests use the physical renderer and
match the compatibility query’s rows, JSON values, columns, and bind safety.

### P4 — Required traversal matches and scoped relationship filters

**Can run in parallel with P3 after P1/P2.**

1. Lower `TraversalMatchMode` and required relationship filters into typed
   `Exists` subplans instead of generated `LENGTH(FOR ...)` strings.
2. Render a bounded `LIMIT 1` correlated traversal with edge/node project,
   generation, auth, label, target edge-type, and resource-type predicates.
3. Reuse the existing proven `storage_route` direction evidence; add explicit
   tests for ResearchSubject -> ResearchStudy outbound traversal.

**Acceptance:** required-match parity, no unscoped edge/node in rendered AQL,
and live Explain coverage for inbound and proven outbound routes.

### P5 — Optional traversal sets and traversal-sharing

**Can start once P1/P2 are stable; completes after P3/P4.**

1. Lower child `SemanticNode`s into typed set subplans, preserving optional
   traversal semantics and root row grain.
2. Materialize sibling-prefix sharing only when route, source, label, scope,
   and traversal direction are equivalent; create typed filtered subsets for
   distinct target resource types.
3. Implement dedupe and deterministic ordering as physical semantics, not
   renderer-side textual rewrites.
4. Add plan goldens proving shared prefixes do not broaden target types or
   alter result order.

**Acceptance:** nested optional traversal fields execute through physical
plans; no regression in root row count; shared-prefix Explain/cost coverage.

### P6 — Aggregates, derived fields, and representative slices

**Depends on P3/P5.**

1. Lower `SemanticAggregate`, `SemanticSlice`, and derived field operations
   directly from semantic nodes/typed sets.
2. Specify empty-set behavior for every aggregate and derive it from current
   result fixtures before implementation.
3. Render only fixed aggregate functions; reject unknown operations during
   physical validation.
4. Ensure every representative slice has stable sort, bounded limit, and
   nested typed projection.

**Acceptance:** GDC-style counts, value lists, representative samples/files,
and derived fields have exact result parity on META fixtures.

### P7 — Pivots and shaped object projections

**Depends on P3/P5; can overlap P6.**

1. Lower semantic pivots to `PivotMap` expressions with validated permitted
   columns, key/value selectors, and collision semantics.
2. Render pivot maps with bind-backed column names; never synthesize AQL keys
   from data or request strings.
3. Compose fields, aggregates, pivots, slices, and child-derived values into a
   typed return object in requested column order.

**Acceptance:** complex GDC dataframe fixtures with samples/files/groups/
observations and pivots execute only through the physical renderer and retain
column shape exactly.

### P8 — Performance, Explain, and observability gates

**Runs continuously after P3; owns final performance sign-off.**

1. Add a named real-world GDC dataframe fixture under `examples/` or
   `conformance/compiler/`, with human-readable GraphQL input and expected
   NDJSON/column contract.
2. Add cold/warm benchmark commands that report compile time, Arango query
   execution time, rows, bytes, and rows/second separately.
3. For each physical feature, require live Arango `EXPLAIN` assertions:
   no full collection scans, scoped root index use, traversal edge index use,
   and bounded estimated cost relative to fixture cardinality.
4. Add a physical-plan explanation that reports operator counts, traversals,
   shared prefixes, and selected indexes without exposing raw AQL internals.

**Acceptance:** documented benchmark baseline and regression threshold for the
representative GDC dataframe; failures identify compiler phase versus Arango
execution phase.

### P9 — Shadow parity and cutover

**Depends on P3–P8.**

1. Build a test-only harness that compiles each fixture through both the
   physical path and `compileLowered`, executes both against the same Arango
   generation, canonicalizes row-object key order, and compares rows/columns.
2. Cover root fields, nested fields, all filters, required matches, inbound and
   outbound routes, aggregates, slices, pivots, auth scopes, empty results,
   generation scope, and preview limits.
3. Make `CompileRequest` select the physical route for one capability family at
   a time only after that family has parity and Explain gates.
4. Remove the fallback condition only when the full conformance matrix uses
   physical plans.

**Acceptance:** zero compatibility fallbacks in conformance; physical path is
the default for every supported GraphQL dataframe request.

### P10 — Delete the compatibility compiler

**Depends on P9. Must be one focused deletion PR.**

1. Delete `compileLowered`, `compiler`, `NamedSet`, `DerivedField`, and other
   lowered-only types/helpers that have no non-test consumer.
2. Delete `lowerSemanticBuilder` and old lowered-query goldens.
3. Simplify `CompileRequest` to semantic validation -> physical lowering ->
   rendering. Preserve the public GraphQL input/output contract.
4. Run deadcode with tests, full Go tests, compiler conformance, integration
   Explain tests, and benchmark gate.

**Acceptance:** no production reference to `Builder.Sets`, `compileLowered`,
or lowered AQL string helpers; one compiler execution path remains.

## Parallelization and merge order

| Wave | Work packages | Shared files / merge owner |
| --- | --- | --- |
| 0 | P1, fixture/benchmark design from P8 | `physical_plan.go`: P1 owner |
| 1 | P2, P3, P4 | `physical_lowering.go`: P2 owner; P3/P4 add isolated feature files |
| 2 | P5, P6, P7, P8 | `physical_render.go`: renderer owner serializes reviewed IR additions |
| 3 | P9 | `compile.go`: cutover owner |
| 4 | P10 | deletion owner, after green matrix only |

No worker edits `compile.go` while P1–P8 are in flight. Feature workers add
IR/lowering tests and renderer tests; the renderer owner integrates accepted
operation kinds. This prevents one feature from silently reintroducing
string-oriented lowering.

## Explicit non-goals

- Do not add unproven FHIR traversal directions merely to increase coverage.
- Do not optimize by materializing new collections before physical plans can
select and explain them.
- Do not alter GraphQL input/output names during this migration.
- Do not delete the compatibility compiler before live result parity, not just
  query-string similarity, is proven.
