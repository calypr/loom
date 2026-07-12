# Luna Execution Plan: Remove the Lowered AQL Compiler

## Mission

Make the physical compiler the only runtime execution path for supported
dataframe requests. The final runtime pipeline is:

```text
GraphQL input
  -> graphqlapi/dataframe
  -> dataframe.Builder
  -> dataframe.BuildSemanticPlan
  -> dataframe.BuildPhysicalPlan
  -> dataframe.RenderPhysicalPlan
  -> Arango AQL
```

Delete the runtime compatibility path (`lowerSemanticBuilder`, `Compile`, and
`compileLowered`) only after the physical renderer covers the same supported
semantic operations and has result parity. Do not preserve old AQL helpers for
backward compatibility once their physical replacement is enabled.

This is an implementation plan, not a request to invent FHIR behavior. The
repository already contains the FHIR facts and generated backbone:

- `fhirstructs/`: generated FHIR structs, validators, and edge extraction;
- `fhirschema/`: generated resource metadata, selectors, and traversals;
- `META/`: loaded sample FHIR NDJSON data;
- `graphqlapi/schema.graphqls`: current public GraphQL contract;
- `internal/dataframe/storage_route.go`: the only authority for physically
  proven FHIR edge directions;
- `internal/dataframe/semantic_plan.go`: semantic request meaning;
- `internal/dataframe/physical_plan.go`: renderer-independent physical IR.

Never add a hard-coded resource-specific FHIR route or field just to make a
fixture pass. Resolve fields through `fhirschema` and routes through
`resolveStorageRoute`.

## Current baseline (verify before editing)

The physical runtime currently owns:

- root scan plus project, dataset-generation, and authorization scope;
- root sort and preview limit;
- root field extraction, selector fallbacks, scalar/array/distinct-array
  values, and root typed filters;
- optional navigation-only traversal sets;
- optional child traversal sets with child field projections and typed child
  filters, including nested child sets;
- root and child aggregates (COUNT, COUNT_DISTINCT, EXISTS, DISTINCT_VALUES,
  MIN, MAX, and predicate-qualified reductions);
- required traversal matches as scoped correlated `EXISTS` subplans;
- proven inbound routes and the explicit outbound
  `ResearchSubject -> ResearchStudy` route.

The compatibility compiler still owns:

- derived fields;
- pivots;
- representative slices;
- legacy named-set traversal sharing.

Before work, run:

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./... -count=1
```

If the checkout has unrelated dirty changes, preserve them. Do not reset or
reformat unrelated code.

## Non-negotiable compiler invariants

Every work package must maintain these invariants:

1. One outer `FOR root` produces at most one row per root resource.
2. `SORT root._key` and optional `LIMIT @limit` occur before optional child
   traversal work. Required semi-joins occur before that window.
3. Every participating root, child node, and `fhir_edge` document has project,
   dataset-generation, and authorization predicates.
4. AQL contains only compiler-owned syntax. Request values, field names,
   resource types, labels, projection names, and pivot keys are bind values or
   generated metadata; never concatenate request data into AQL source.
5. Physical plan validation rejects unsafe bind keys, selector paths, variable
   capture/scope violations, untyped collection binds, and missing return
   values before rendering.
6. Fallback/value-mode behavior is part of the output contract. Preserve
   scalar `null`, empty arrays, array order, and distinct semantics as proven
   by existing compatibility tests.
7. Unknown outbound or `ANY` routes fail through `resolveStorageRoute`; do not
   make them work by guessing edge direction.

## Physical IR rules

Use the existing typed IR. Add fields only when a semantic concept cannot be
expressed safely with the current structures:

- `PhysicalSet` / `PhysicalSubplan`: correlated array-valued child set;
- `PhysicalExpression`: value, extract, aggregate, pivot map, slice, object;
- `PhysicalPredicateExpression`: comparison, all, any, not, exists;
- `PhysicalExtract`: parsed selector plus fallbacks and explicit cardinality;
- `PhysicalAggregate`, `PhysicalPivotMap`, `PhysicalSlice`.

Do not reintroduce `NamedSet`, string expressions, or an AQL snippet field to
the physical plan. If an operation cannot be rendered without a raw string,
the IR is incomplete and must be extended first.

## Work package 1 — Make physical subplans fully executable

**Files owned**

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_helpers.go`
- `internal/dataframe/physical_plan_test.go`

**Goal**

Finish the generic support for `PhysicalSet` and nested `PhysicalSubplan`
before semantic lowering starts producing them for real child fields.

**Implementation steps**

1. Make `clonePhysicalPlan` deeply clone every rich IR payload: expressions,
   predicate trees, sets, subplans, selectors, projections, and bind values.
   `withGenericPhysicalExecutionWindow` must not mutate its input plan.
2. In `PhysicalPlan.Validate`, validate set captures against the parent lexical
   scope. A subplan may reference only captures and variables it defines.
3. Reject root scans, outer returns, sort, and preview limits inside a child
   subplan. A child subplan can contain traversal, scoped filters, derived lets,
   nested sets, and a typed return expression.
4. Extend collection-bind discovery and render validation recursively through
   sets, exists predicates, aggregate/slice/pivot expressions, and nested
   object projections.
5. Implement deterministic AQL rendering for `PhysicalSet`:

   ```aql
   LET child_set = (
     FOR child, edge IN 1..1 INBOUND parent @@edge_collection
       ... mandatory scope filters ...
       RETURN child
   )
   ```

   Apply `UNIQUE` only when `PhysicalSet.Unique` is true. Do not emit a top
   level child loop.

**Tests**

- deep-clone mutation test for every rich payload;
- capture rejection, future-variable rejection, and nested-scope rejection;
- collection bind inside nested set/existence validation;
- renderer golden proving nested sets remain under root `LET` expressions.

**Done when**: a hand-built nested child-set physical plan validates and renders
without raw AQL payloads, and `go test ./internal/dataframe -run TestPhysical`
passes.

## Work package 2 — Lower optional child nodes to physical sets

**Files owned**

- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_lowering.go`
- `internal/dataframe/physical_helpers.go`
- new `internal/dataframe/physical_child_set_test.go`

**Goal**

Replace navigation-only child traversals with a physical set hierarchy that is
usable by fields, filters, aggregates, pivots, and slices.

**Implementation steps**

1. Add an internal lowering context with deterministic counters for set/node/
   edge variable names and bind keys. Do not derive names from user aliases.
2. For each optional `SemanticNode`, call:

   ```go
   resolveStorageRoute(parent.ResourceType, child.EdgeLabel, child.ResourceType)
   ```

   Populate a `PhysicalSet` with a subplan that captures the parent variable
   (or parent set-element variable for nesting).
3. Inside each set subplan, emit in this order:
   - one-hop typed `PhysicalTraversal`;
   - edge and child project filters;
   - edge and child dataset-generation filters;
   - edge and child authorization filters;
   - child resource type and edge target-type discriminator filters;
   - child typed filters (work package 4);
   - nested child sets (recursive call);
   - typed return object/value for the owning projection.
4. Preserve semantic request ordering. Grouping or traversal sharing is not
   allowed in this package.
5. Update `genericPhysicalNodeUnavailableReason` only for the exact feature
   implemented. Do not mark pivots, aggregates, slices, or derived fields as
   physical merely because a child set exists.

**Tests**

- Patient -> Specimen field;
- Patient -> Specimen -> DocumentReference nested field;
- root row count remains one with zero/many children;
- inbound and ResearchSubject -> ResearchStudy outbound route rendering;
- unknown forward route remains rejected.

**Done when**: optional child field-only requests select `BuildPhysicalPlan`
and no longer invoke `lowerSemanticBuilder` at runtime.

## Work package 3 — Project fields from child sets

**Files owned**

- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/selection_semantics.go` only if an existing semantic
  contract is missing
- `internal/dataframe/physical_child_projection_test.go`

**Goal**

Emit exactly the current output shapes for traversed selections.

**Implementation steps**

1. Reuse `ResolveSemanticField(resourceType, nodeAlias, index, field)` for
   every child `SemanticField`. Do not duplicate its selection semantics.
2. Lower a child field to `PhysicalExtract` with source equal to the child set
   element payload and use explicit cardinality/null behavior.
3. For a child field output, render one typed subquery over the set:
   - scalar/first: `FIRST(...)` according to semantic projection;
   - array: ordered values;
   - distinct array: physical distinct operation followed by required stable
     ordering, matching existing tests.
4. Nested child fields are evaluated inside their parent child-set subplan, not
   by reopening an uncorrelated root traversal.
5. Add projection names to `CompiledQuery.Columns` from the final physical
   return operation. Do not reconstruct columns from `Builder` after lowering.

**Parity fixtures**

Use existing `META` resource types and add a physical fixture that selects:

- specimen type from a patient-linked specimen;
- DocumentReference attachment title from a specimen-linked file;
- an array-valued field and a fallback field.

Compare legacy and physical rows after canonical JSON key ordering. Compare
columns and row values, not raw AQL text.

**Done when**: child field outputs have live Arango result parity and all
field-only traversal requests run physically.

## Work package 4 — Child filters and required-match filters

**Files owned**

- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_required_match.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_child_filter_test.go`
- `internal/dataframe/physical_required_match_test.go`

**Goal**

Use one typed predicate lowering path for root filters, child set filters, and
required-match subplan filters.

**Implementation steps**

1. Extract root predicate construction from `appendRootPhysicalFilters` into
   `lowerPhysicalFilters(resourceType, sourcePayload, filters, bindVars,
   bindPrefix)`. It returns typed `PhysicalPredicateExpression`s and owns all
   bind keys.
2. Invoke the helper in child set subplans immediately after mandatory scope
   filters and before subplan return.
3. Invoke the same helper in `buildRequiredTraversalExists`; remove its current
   rejection of `node.Filters`.
4. Retain exact existing `TypedFilter` behavior: `EQUALS`, `NOT_EQUALS`, `IN`,
   `CONTAINS_TEXT`, `GT`, `GTE`, `LT`, `LTE`, `EXISTS`, `MISSING`, and
   ALL/ANY/NONE. Use parsed selectors and bind literals only.
5. For date/datetime comparison, preserve existing `DATE_TIMESTAMP` behavior
   from the compatibility compiler.

**Tests**

- every filter operation at child depth one;
- nested child filter;
- required traversal filter in both inbound and proven outbound directions;
- scope test where a matching document is out of auth scope or wrong dataset
  generation;
- live `EXPLAIN` test checks a traversal edge index and no full collection scan.

**Done when**: `compileTypedFilters` has no production call site outside the
compatibility oracle and all field/filter traversal requests run physically.

## Work package 5 — Aggregates and derived fields

**Files owned**

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- new `internal/dataframe/physical_aggregate_test.go`

**Goal**

Lower `SemanticAggregate` and supported derived fields over typed physical
sets, with no use of `Builder.Sets` or `DerivedField` in runtime execution.

**Implementation steps**

1. Map every `SemanticAggregate.Operation` currently accepted by validation to
   a `PhysicalAggregateOperation`. Before coding, enumerate the accepted
   operations from `semantic_validation.go` and write a table-driven test that
   rejects an unmapped operation.
2. Lower aggregate selectors and optional predicates with the shared helpers
   from work packages 3 and 4.
3. Specify output behavior from existing compatibility tests before rendering:
   - count/distinct count: `0` on empty set;
   - values/distinct values: `[]` on empty set;
   - first/min/max: `null` on empty set unless current test behavior differs.
4. Render fixed aggregate AQL templates only. Do not store function names in
   request data. Ensure distinct arrays retain required ordering.
5. Lower derived count/values/first operations as typed expressions referencing
   a physical set variable or prior physical expression. Do not permit an AQL
   variable name from `Builder` to cross the boundary.

**Tests**

- zero/one/many child documents;
- repeated values and distinct values;
- predicate-qualified count;
- `META` GDC case -> specimen -> file counts/values;
- physical-vs-compat execution parity.

**Deletion gate**

Delete the corresponding production branches in `compileRootAggregateExpr` and
`compileDerivedField` after all aggregate fixtures select physical execution.

## Work package 6 — Pivots

**Files owned**

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- new `internal/dataframe/physical_pivot_test.go`

**Goal**

Make root and child `SemanticPivot` physical without string-generated object
keys.

**Implementation steps**

1. Lower each pivot to `PhysicalPivotMap` with typed source, resource type,
   parsed key/value selectors, and a bind-backed permitted column list.
2. Establish collision behavior by executing the existing compatibility pivot
   tests; encode it as IR/renderer behavior and test duplicates explicitly.
3. Render a fixed AQL pivot subquery that filters values to permitted columns.
   Requested output column names are bind values.
4. Set `CompiledQuery.PivotFields` from physical return projection metadata,
   not from the old builder.

**Tests**

- root and child pivot;
- sparse values, duplicate key, absent permitted key, nested array selector;
- real observation pivot fixture from `META`;
- bind-safety test: no requested key appears interpolated in AQL source.

**Deletion gate**

Remove `compileRootPivot`, `compileDerivedPivotMapLets`, and their production
call sites after parity is green.

## Work package 7 — Representative slices

**Files owned**

- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_plan.go` only if required
- new `internal/dataframe/physical_slice_test.go`

**Goal**

Lower `SemanticSlice` to a bounded typed local subquery.

**Implementation steps**

1. Lower the source child set, optional typed predicate, sort expression,
   positive bind-backed limit, and nested projection to `PhysicalSlice`.
2. Require an explicit deterministic sort with a tie breaker. Do not inherit
   incidental collection or `UNIQUE` order.
3. Render `FILTER`, `SORT`, `LIMIT`, and object projection inside the slice
   subquery. Never alter the outer root preview window.
4. Validate limit type/positivity in physical plan validation.

**Tests**

- zero/one/many children;
- equal primary sort values prove tie stability;
- predicate + slice interaction;
- nested field object projection;
- output parity with existing representative slice fixture.

**Deletion gate**

Delete `compileRepresentativeSlice`, `compileRootSlice`, and slice-only legacy
tests once all slice fixtures are physical.

## Work package 8 — Traversal sharing optimization

**Dependencies**: work packages 2–7 must already have unshared physical parity.

**Files owned**

- new `internal/dataframe/physical_optimize.go`
- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_render.go` only if an existing set form cannot
  render the optimized plan
- new `internal/dataframe/physical_optimize_test.go`

**Goal**

Recreate generic sibling-prefix sharing as a physical-plan optimization,
without changing semantic results.

**Implementation steps**

1. Match only sets with identical parent capture, direction, edge collection,
   label, project/generation/auth scopes, and traversal depth.
2. Materialize one broad shared set only when children differ solely by target
   resource type. Create typed filtered subsets for each target type.
3. Run this optimizer after semantic lowering and before rendering. Keep an
   unoptimized plan available for deterministic test comparison.
4. Record sharing count and set count in physical explain metadata; do not
   expose raw AQL variable names to GraphQL clients.

**Tests and performance gate**

- optimized/unoptimized result parity;
- no target type broadening;
- `EXPLAIN` confirms edge traversal index use;
- benchmark must be cost-neutral or better before enabling optimization.

## Work package 9 — Production cutover and deletion

**Preconditions**

- all supported semantic features have physical parity;
- full `META` fixture matrix executes through `CompileRequest` without
  `lowerSemanticBuilder`;
- live Arango explain/cost coverage is green;
- one real GDC-style GraphQL request produces a useful dataframe (fields,
  child fields, counts, pivots, slices) through the physical path.

**Implementation steps**

1. Change `CompileRequest` to exactly:

   ```go
   semantic, err := BuildSemanticPlan(builder)
   if err != nil { return CompiledQuery{}, err }
   physical, err := BuildPhysicalPlan(semantic)
   if err != nil { return CompiledQuery{}, err }
   return compilePhysicalExecution(physical, semantic, limit)
   ```

2. Move compatibility execution comparison into test-only helpers. It may be
   retained temporarily in a test package as an oracle, but it must have no
   production caller.
3. Delete production `Compile`, `Lower`, `lowerSemanticBuilder`,
   `compileLowered`, `NamedSet`, and lowered-only helpers/types when deadcode
   proves no production consumers remain.
4. Delete obsolete compatibility-only fixtures and replace them with physical
   plan plus execution-parity fixtures.
5. Run:

   ```bash
   GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./... -count=1
   GOCACHE="$(pwd)/.gocache" deadcode -test ./...
   git diff --check
   ```

6. With local Arango available, run the named Explain tests plus the
   human-readable dataframe command:

   ```bash
   LOOM_COMPILER_ARANGO_INTEGRATION=1 \
   GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto \
   go test ./internal/dataframe -run 'Test.*Explain.*Arango' -count=1 -v

   make dataframe-demo
   ```

## Parallelization rules for Luna

Use one integration owner per wave. `physical_plan.go`,
`physical_render.go`, `generic_physical_plan.go`, and `compile.go` are shared
hotspots and must not be edited concurrently by multiple workers.

| Wave | Worker A | Worker B | Integration owner |
| --- | --- | --- | --- |
| 1 | WP1 IR/subplan validation | fixtures and parity harness | WP1 owner |
| 2 | WP2 child set lowering | WP3 child projection tests | WP2 owner |
| 3 | WP4 predicates/required filters | live Explain tests | WP4 owner |
| 4 | WP5 aggregates/derived fields | aggregate parity fixtures | WP5 owner |
| 5 | WP6 pivots | pivot fixtures/benchmark reporting | WP6 owner |
| 6 | WP7 slices | slice fixtures | WP7 owner |
| 7 | WP8 sharing/perf | WP9 deletion audit | compiler core owner |

Workers must not write `compile.go` until work package 9. Each feature worker
reports the exact supported semantic shapes and leaves unsupported shapes
explicitly rejected by `BuildPhysicalPlan`; do not add a new fallback.
