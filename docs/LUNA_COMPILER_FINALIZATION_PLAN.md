# Luna Execution Plan: Finish the Physical Compiler and Delete Compatibility Lowering

## Outcome

Complete the remaining physical compiler features and remove the compatibility
compiler. The final production request path must be:

```text
GraphQL input
  -> graphqlapi/dataframe input mapping
  -> dataframe.Builder
  -> BuildSemanticPlan
  -> BuildPhysicalPlan
  -> physical optimization
  -> RenderPhysicalPlan
  -> Arango execution
```

The following symbols must have no production definition or call site when the
plan is finished:

- `Compile`
- `Lower`
- `lowerSemanticBuilder`
- `compileLowered`
- `NamedSet`
- `DerivedField`
- lowered-only `RepresentativeSlice`
- `compiler` and its string-oriented helper methods

Do not copy old AQL string helpers into the physical renderer. Migrate their
semantic meaning into typed physical operations, prove execution parity, then
delete the old implementation.

## Repository facts Luna must use

This repository already contains the FHIR backbone. Do not infer FHIR paths or
relationships from fixture names.

- `fhirstructs/`: generated FHIR Go structs, validation, and edge extraction.
- `fhirschema/`: generated resource, selector, traversal, and pivot metadata.
- `META/`: real sample FHIR NDJSON.
- `internal/dataframe/storage_route.go`: authoritative physical edge-direction
  evidence. Unknown outbound/ANY routes remain compiler errors.
- `internal/dataframe/semantic_plan.go`: backend-independent request meaning.
- `internal/dataframe/physical_plan.go`: typed renderer-independent execution
  IR.
- `internal/dataframe/generic_physical_plan.go`: current semantic-to-physical
  lowering.
- `internal/dataframe/physical_render.go`: current physical AQL renderer.
- `graphqlapi/schema.graphqls`: public input/output contract.
- `conformance/compiler/fixtures/`: compiler fixtures.

Use `fhirschema` validation for selector/pivot decisions and the populated
catalog for bounded pivot columns. Never add Patient-, GDC-, or fixture-specific
compiler branches.

## Current physical coverage

The physical route currently handles:

- root fields and typed filters;
- optional child fields and typed child filters;
- required traversal matches;
- inbound and proven outbound storage routes;
- root and child aggregates: `COUNT`, `COUNT_DISTINCT`, `EXISTS`,
  `DISTINCT_VALUES`, `MIN`, `MAX`, plus predicate-qualified reductions;
- root execution window and generation/auth/project scope.

The remaining production fallback reasons should be limited to:

- pivots;
- representative slices;
- nested optional shapes that exceed current physical-set nesting;
- any legacy-only `Builder.DerivedFields`/`Builder.Sets` request assembled
  directly by Go callers;
- compatibility traversal-sharing representation.

Before changing code, run and record:

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./... -count=1
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./conformance/compiler -bench BenchmarkCompilerOracle -benchmem -run '^$'
```

Preserve unrelated dirty-worktree changes.

## Compiler invariants

Every work package must retain these properties:

1. One outer root loop, one returned object per root resource.
2. Required semi-joins execute before root `SORT`/`LIMIT`; optional child work
   executes after the root preview window.
3. Every scanned root, child node, and edge is constrained by project,
   dataset generation, and authorization scope.
4. Request values, resource types, edge labels, selectors, pivot keys, limits,
   and output names never become unvalidated AQL source.
5. Empty-set, null, scalar, array, distinct-array, and fallback behavior is
   explicit in physical IR and covered by result tests.
6. Nested optional traversal never becomes a top-level `FOR` and never
   multiplies root rows.
7. Stable ordering is explicit. Do not rely on Arango traversal, `UNIQUE`, or
   hash-map iteration order.
8. Unsupported routes and expressions fail before rendering.

## Work package F0 — Inventory legacy meaning before migration

**Goal:** prove which legacy concepts represent public semantics and which are
only compatibility IR.

**Read-only inputs**

- `internal/dataframe/lowered_types.go`
- `internal/dataframe/planner.go`
- `internal/dataframe/generic_lowering.go`
- `internal/dataframe/lowered_compile.go`
- `graphqlapi/dataframe/input_mapping.go`

**Deliverable:** add a table-driven test or checked-in Markdown table mapping
every legacy operation:

| Legacy operation | Physical owner |
| --- | --- |
| `ROOT_FIELD` | `PhysicalExtract` scalar |
| `FIRST_NON_NULL` | `PhysicalExtract` with fallbacks, scalar |
| `ALL` | `PhysicalExtract` array |
| `UNIQUE` | `PhysicalExtract` distinct array |
| `COUNT` | `PhysicalAggregate(COUNT)` |
| `COUNT_DISTINCT` | `PhysicalAggregate(COUNT_DISTINCT)` |
| `COUNT_WHERE` | predicate-qualified `PhysicalAggregate(COUNT)` |
| `ANY` | predicate-qualified `PhysicalAggregate(EXISTS)` |
| `MIN`/`MAX` | typed physical aggregates |
| `PIVOT` | `PhysicalPivotMap` |
| representative slice | `PhysicalSlice` |
| `CONST` | classify as public requirement or legacy-only artifact |

`CONST` must not be added to the physical compiler unless a current GraphQL
input or production caller requires it. If it exists only in tests/manual
lowered builders, delete that support at final cutover.

**Acceptance:** every `DerivedOp*` constant has an explicit migration or
deletion decision. No worker may create `PhysicalDerivedField` as a generic
replacement for the old union.

## Work package F1 — Eliminate “derived fields” as a runtime concept

**Goal:** ensure every public output currently represented as `DerivedField`
is produced directly from `SemanticNode` by typed physical expressions.

**Files**

- `internal/dataframe/semantic_plan.go`
- `internal/dataframe/selection_semantics.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_plan.go`
- new `internal/dataframe/physical_derived_parity_test.go`

**Implementation**

1. Enumerate all places where `planner.go` creates `DerivedField`.
2. For field value modes:
   - `FIRST`/`AUTO` -> scalar `PhysicalExtract` with ordered fallbacks;
   - `ALL` -> array `PhysicalExtract`;
   - `DISTINCT` -> distinct-array `PhysicalExtract`.
3. For aggregate-derived outputs, use `SemanticAggregate` and
   `PhysicalAggregate`; do not lower them through `DerivedField`.
4. For pivot-derived outputs, wait for F2 and lower from `SemanticPivot`.
5. For representative outputs, wait for F3 and lower from `SemanticSlice`.
6. Ensure `CompiledQuery.Columns` and `PivotFields` come from final physical
   return metadata, not compatibility builder arrays.
7. Add a test that builds representative public GraphQL shapes, runs
   `BuildSemanticPlan`, and proves the physical plan contains no dependency on
   `NamedSet` or `DerivedField`.

**Parity matrix**

- root scalar/array/distinct/fallback field;
- child scalar/array/distinct/fallback field;
- count/count-where/any/min/max;
- empty child set and all-null selector values.

**Deletion gate:** after parity, remove production reads of
`Builder.DerivedFields` from `CompileRequest` routing. Directly constructed
legacy builders may remain test-only until F7.

## Work package F2 — Physical pivots

**Goal:** lower root and child `SemanticPivot` directly to `PhysicalPivotMap`
and render a bounded object without request-derived AQL keys.

**Files**

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_execution.go`
- `internal/dataframe/pivots.go`
- new `internal/dataframe/physical_pivot_test.go`
- optional `internal/dataframe/physical_pivot_arango_integration_test.go`

**Preconditions**

- `Service.expandPivotColumns` has populated a non-empty bounded column list.
- `BuildSemanticPlan` has validated both selectors through
  `fhirschema.ValidatePivotSelectors`.

**IR changes**

Review the existing `PhysicalPivotMap`. Extend it only if needed to encode:

- source cardinality: root singleton vs physical child set;
- collision behavior;
- empty/absent column behavior;
- output column order;
- optional value distinctness if required by current behavior.

Do not add a raw map/AQL expression field.

**Lowering**

1. Root pivot source is the root document payload.
2. Child pivot source is the owning `PhysicalSet` variable.
3. Copy parsed key/value selectors and bounded columns into typed IR.
4. Store the column list in a bind variable with a deterministic compiler key.
5. Add the pivot expression to the final return using the semantic name and
   mark the projection as a pivot for `CompiledQuery.PivotFields`.

**Rendering**

Render a fixed subquery equivalent to:

```aql
MERGE(
  FOR item IN source
    FOR key IN key_values
      FILTER key IN @allowed_columns
      LET values = value_values
      COLLECT pivot_key = key INTO grouped
      RETURN { [pivot_key]: stable_collision_value(grouped) }
)
```

Use the existing legacy tests to determine the exact collision value. Do not
choose `FIRST`, `LAST`, or array aggregation without parity evidence.

**Tests**

- root and child pivots;
- sparse column set;
- duplicate key with multiple values;
- array key selector and array value selector;
- empty result object;
- requested keys absent from data;
- bind-safety test proving column strings do not appear in AQL source;
- deterministic output/pivot-field ordering.

**Live gate:** run a real Observation pivot over `META`, compare legacy and
physical JSON rows, and require no full collection scan plus traversal-edge
index use.

**Deletion gate:** remove production calls to `compileRootPivot`,
`compilePivotField`, `compileDerivedPivotMapLets`,
`compilePivotMapExpr`, and `compilePivotMapProjection`.

## Work package F3 — Physical representative slices

**Goal:** lower `SemanticSlice` to a stable, bounded `PhysicalSlice` for root
and child sources.

**Files**

- `internal/dataframe/physical_plan.go`
- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_render.go`
- new `internal/dataframe/physical_slice_test.go`
- optional `internal/dataframe/physical_slice_arango_integration_test.go`

**IR contract**

`PhysicalSlice` must encode:

- typed source;
- optional typed predicate;
- deterministic primary sort and `_key` tie breaker;
- positive bind-backed limit;
- ordered nested object projections;
- empty-set behavior (`[]`).

If the current `Sort` field cannot express primary plus tie-break ordering,
replace it with an ordered `[]PhysicalSortKey`; do not rely on implicit order.

**Lowering**

1. Root slice source is the singleton root document.
2. Child slice source is the physical set for its semantic node.
3. Reuse the typed selector/filter lowering used by child filters and aggregate
   predicates.
4. Lower each `SemanticSlice.Fields` entry via `ResolveSemanticField`.
5. Bind the limit. Reject zero/negative limits in semantic or physical
   validation.

**Rendering order**

```aql
FOR item IN source
  FILTER typed_predicate
  SORT typed_primary, item._key
  LIMIT @slice_limit
  RETURN { bound_name: typed_projection, ... }
```

This subquery belongs inside the returned root object and must not affect the
outer root limit.

**Tests**

- zero/one/many source items;
- deterministic tie behavior;
- predicate with and without equality literal;
- scalar/fallback/array nested fields;
- root and child slice;
- auth/generation-scoped source;
- limit is a bind, not an AQL literal.

**Deletion gate:** remove production calls to `compileRepresentativeSlice`,
`compileRootSlice`, `compileSetSlice`, `compileSliceProjection`, and their
predicate-string helpers after all slice fixtures use physical execution.

## Work package F4 — Fully general nested optional shaping

**Goal:** allow any supported field/filter/aggregate/pivot/slice at any proven
optional traversal depth without reopening traversal paths or multiplying root
rows.

**Files**

- `internal/dataframe/generic_physical_plan.go`
- `internal/dataframe/physical_plan.go`
- `internal/dataframe/physical_render.go`
- `internal/dataframe/physical_scope.go`
- new `internal/dataframe/physical_nested_shape_test.go`

**Current limitation to remove**

Delete every `genericPhysicalNodeUnavailableReason` or lowering rejection for a
child that simultaneously has nested children and selections/shaping.

**Lowering model**

1. Each optional semantic child owns exactly one `PhysicalSet`.
2. A nested set captures the parent set-element variable within the parent
   subplan. It must not capture the entire parent set and re-flatten unrelated
   rows.
3. The owning child output is a typed `PhysicalObject` containing fields,
   aggregates, pivots, slices, and nested child objects/arrays.
4. Parent output cardinality is explicit:
   - child array projection remains an array;
   - scalar representative uses the semantic value mode;
   - nested child results remain correlated to their parent item.
5. Route resolution happens once per semantic edge through
   `resolveStorageRoute`.

**Scope proof**

Extend `PhysicalPlan.Validate` and `ValidateGenericPhysicalPlanScope` to walk
nested subplans recursively. Every traversal inside a subplan must prove edge
and node project/generation/auth predicates before its return or nested set.

**Required tests**

- Patient -> Specimen -> DocumentReference fields and aggregates;
- Patient -> Specimen -> Group -> DocumentReference;
- mixed optional and required branches;
- same alias at different depths does not collide;
- zero/many children preserve root count;
- nested outbound ResearchSubject -> ResearchStudy;
- invalid capture and out-of-scope variable rejection.

**Acceptance:** the checked-in GDC-style dataframe runs physically with no
unsupported nested-shaping reason.

## Work package F5 — Traversal-prefix sharing optimization

**Goal:** optimize equivalent physical traversal prefixes after unoptimized
plans have full parity.

**Files**

- new `internal/dataframe/physical_optimize.go`
- new `internal/dataframe/physical_optimize_test.go`
- `internal/dataframe/physical_execution.go`
- physical explain/benchmark files

**Placement**

```text
SemanticPlan -> BuildPhysicalPlan -> OptimizePhysicalPlan -> RenderPhysicalPlan
```

The semantic lowerer must always be capable of producing a correct unshared
plan. Sharing is never performed while walking `SemanticNode`.

**Equivalence key**

Two set/traversal prefixes can share only when all match:

- parent capture identity;
- depth;
- direction;
- edge collection;
- edge label;
- project bind;
- dataset-generation bind;
- authorization scope inputs;
- filter prefix before target-type specialization;
- target edge-type discriminator field.

Do not include target resource type in the broad-prefix key. Instead, only
after sharing, create typed subsets with both edge discriminator and node
`resourceType` filters.

**Rewrite**

1. Hash eligible prefixes by the equivalence key.
2. Keep groups with at least two consumers and at least two target types.
3. Introduce one broad set with a compiler-generated variable.
4. Rewrite each consumer to a typed filtered subset.
5. Preserve consumer request order and source provenance.
6. Re-run physical validation after rewriting.

**Tests**

- sibling sharing at root and nested depth;
- differing labels/directions/scopes/filters never share;
- same label but one target type does not create useless broad set;
- optimized/unoptimized execution parity;
- stable deterministic plan across runs;
- no target-type leakage.

**Performance gate:** live Explain estimated cost and measured warm execution
must be neutral or better on the GDC fixture. Disable the rewrite if it
regresses the representative query.

**Deletion gate:** remove compatibility `NamedSet` traversal/filter/union and
generic sibling-sharing lowering only after physical sharing owns equivalent
cases.

## Work package F6 — Result parity and cost benchmark matrix

**Goal:** make compiler correctness and performance measurable before deleting
the oracle.

**Files**

- `internal/dataframe/physical_renderer_parity_test.go`
- new `internal/dataframe/physical_rich_arango_integration_test.go`
- `conformance/compiler/fixtures/`
- `conformance/compiler/benchmark_test.go`
- `cmd/dataframe-query/`
- `examples/`

**Fixture families**

1. root fields/filters;
2. child fields/filters;
3. required inbound and outbound match;
4. aggregates with empty/non-empty/predicate cases;
5. root and child pivots;
6. root and child slices;
7. three- and four-hop nested shaping;
8. shared sibling prefixes;
9. restricted, unrestricted, and restricted-empty auth scopes;
10. legacy-null and explicit dataset generations.

**Result parity harness**

For each fixture:

1. compile through physical path;
2. compile through compatibility oracle while it still exists;
3. execute both against the same database and generation;
4. canonicalize only JSON object-key ordering;
5. compare row count, row order, columns, pivot fields, scalar/null/array/object
   values, and duplicate behavior;
6. print the first semantic difference with fixture ID and physical operator
   provenance.

Do not sort returned rows unless the public contract declares order irrelevant.

**Explain gates**

- no full collection scan;
- scoped root persistent index selected;
- `fhir_edge` edge index selected for every traversal;
- no warning;
- estimated item count does not explode between optional nested stages;
- shared plan estimated cost is neutral or better than unshared.

**Human-readable benchmark output**

`cmd/dataframe-query` must report separately:

- HTTP/server total;
- compile time if exposed by response/diagnostics;
- Arango execution time if exposed;
- rows;
- response bytes;
- rows/second;
- cold first request and warm subsequent request timings.

Check in a proper GDC-style request under `examples/` that includes fields,
child files, aggregates, observation pivot, representative slices, and nested
groups.

## Work package F7 — Remove production fallback

**Preconditions**

- all supported GraphQL shapes compile physically;
- `genericPhysicalPlanUnavailableReason` has no supported-shape branch;
- F6 parity and Explain matrix is green;
- the GDC-style request executes physically.

**Implementation**

1. Replace `CompileRequest` with a single path:

   ```go
   semantic, err := BuildSemanticPlan(builder)
   if err != nil { return CompiledQuery{}, err }
   physical, err := BuildPhysicalPlan(semantic)
   if err != nil { return CompiledQuery{}, err }
   physical, err = OptimizePhysicalPlan(physical)
   if err != nil { return CompiledQuery{}, err }
   return compilePhysicalExecution(physical, semantic, limit)
   ```

2. Remove `compileRequestPlans` and every runtime fallback call.
3. Change unsupported semantics to explicit errors from semantic or physical
   validation, not compatibility execution.
4. Retain the compatibility compiler only long enough for F6 tests; do not
   expose it through runtime/service code.

**Acceptance:** `rg` finds no production path from service/GraphQL to
`compileLowered`, `Lower`, or lowered builder types.

## Work package F8 — Delete compatibility implementation

Perform this as one focused deletion after F7 is green.

**Delete or empty by deadcode evidence**

- `internal/dataframe/lowered_compile.go`
- `internal/dataframe/lowered_types.go`
- compatibility sections of `planner.go`, `generic_lowering.go`,
  `compile_fields.go`, relationship-match compilation, and storage-route
  adapters;
- old `compiler` struct and AQL string helpers;
- legacy named-set optimization tests;
- compatibility-only fixtures.

**Remove fields from `Builder` and `TraversalStep`**

- `Sets`
- `DerivedFields`
- `RepresentativeSlices`
- compatibility plan hints that no longer describe public diagnostics.

Do not remove public GraphQL fields, aggregates, pivots, or slices. Remove only
the lowered duplicates.

**Test migration**

- rewrite tests that assert legacy variable names or AQL substrings to assert
  physical operators, bind safety, result parity, or Explain behavior;
- keep precise AQL tests only at renderer boundaries;
- remove direct `Lower -> Compile` conformance calls and use
  `CompileRequest`/physical plan fixtures.

**Final commands**

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./... -count=1
GOCACHE="$(pwd)/.gocache" deadcode -test ./...
git diff --check
```

With local Arango:

```bash
LOOM_COMPILER_ARANGO_INTEGRATION=1 \
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto \
go test ./internal/dataframe -run 'Test.*Arango' -count=1 -v

make dataframe-demo
```

**Final source audit**

```bash
rg -n 'compileLowered|lowerSemanticBuilder|type NamedSet|type DerivedField|Builder\.Sets|Builder\.DerivedFields' .
```

Expected result: no production definitions or call sites. Test migration notes
may mention removed symbol names only in historical documentation.

## Luna merge order and worker ownership

The compiler hotspots are:

- `physical_plan.go`
- `generic_physical_plan.go`
- `physical_render.go`
- `physical_execution.go`
- `compile.go`

Only one integration owner edits those files per wave.

| Wave | Primary implementation | Parallel safe work | Integration owner |
| --- | --- | --- | --- |
| 0 | F0 inventory | fixture enumeration | compiler core |
| 1 | F1 semantic derived migration | parity cases | physical lowerer |
| 2 | F2 pivots | pivot fixtures/live data inspection | pivot worker |
| 3 | F3 slices | slice fixtures | slice worker |
| 4 | F4 nested shaping | nested parity fixtures | physical lowerer |
| 5 | F5 sharing optimizer | Explain/cost harness | optimizer worker |
| 6 | F6 matrix | benchmark CLI/example | test owner |
| 7 | F7 cutover | deletion inventory | compiler core |
| 8 | F8 deletion | docs cleanup | deletion owner |

Workers contributing fixtures must not edit compiler hotspot files. F7 and F8
are serialized: do not delete the oracle while a parity worker still uses it.

## Definition of done

- Pivots and slices execute through typed physical expressions.
- Any supported shape can nest at any proven traversal depth without changing
  root grain.
- Physical traversal sharing is validated and benchmark-backed.
- Rich-shape parity and Explain gates pass against `META`.
- A human-readable GDC dataframe request runs physically and reports useful
  timing/row statistics.
- Production contains one compiler path.
- Compatibility lowering, named sets, derived-field IR, and string compiler
  helpers are deleted.
