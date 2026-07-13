# Rich Physical Renderer Plan

## Goal

Finish the physical renderer for the dataframe features that still execute
through `compileLowered` today:

1. optional child traversal projections and filters;
2. aggregates and derived values over those child sets;
3. pivots;
4. representative slices;
5. deletion of the corresponding compatibility compiler code.

The runtime rule at completion is simple:

```text
Builder -> SemanticPlan -> PhysicalPlan -> RenderPhysicalPlan -> AQL
```

`compileLowered` is an oracle during implementation only. It must not receive
new product features or remain a production fallback after its physical
equivalent is available.

## Already physical

- root scan, project/generation/auth scope, root sort/limit;
- root fields, selector fallback/value modes, and root typed filters;
- optional navigation-only traversal sets;
- required traversal matches as correlated, scoped `EXISTS` subplans;
- proven inbound routes and ResearchSubject -> ResearchStudy outbound route.

## Semantic contract to preserve

For every work package, preserve all of the following before cutover:

- one output row per root resource;
- root sort and preview limit happen before optional traversal work;
- child traversals never multiply root rows;
- every node and edge has project, dataset-generation, and auth predicates;
- selector fallback, scalar/array/distinct-array, null, and empty-array
  behavior match the existing output contract;
- output column names and request order are stable;
- no request-derived text is serialized as AQL source.

## W1 — Child traversal sets and child field projections

### Physical IR

Use the existing `PhysicalSet` plus `PhysicalSubplan` for every optional child
node. A child set captures its parent variable, contains its typed traversal
and scope operations, and returns the child node. Add a typed set projection
expression that accepts:

- source set variable;
- child resource type;
- `PhysicalExtract` expression(s);
- scalar/array/distinct-array value mode;
- explicit null/empty behavior.

Nested traversal sets capture their parent set element rather than the root.
They must be defined inside the parent set subplan, not as a top-level AQL
`FOR`, so root row grain cannot change.

### Lowering

1. Lower each optional `SemanticNode` to a `PhysicalSet` in request order.
2. Resolve the route exclusively with `storage_route.go`; reject unknown
   outbound/ANY routes.
3. Lower each child `SemanticField` through `ResolveSemanticField`, exactly as
   root fields are lowered today.
4. Add child output expressions to the owning root/parent projection.
5. Lower sibling-prefix sharing only after unshared child projections have
   exact parity. Sharing is a physical optimization, not a semantic shortcut.

### Renderer

Render a child set as `LET set = (FOR node, edge IN ... RETURN node)` with all
scope filters in the subquery. Render child projections by iterating only over
the set variable. Never expand a child `FOR` in the outer root query.

### Tests and deletion

- unit goldens: one child field, nested child field, array and fallback field;
- result parity on META for patient -> specimen -> file;
- Explain: root scoped index + `fhir_edge` traversal index;
- cut over child-field requests;
- delete the matching `compileTraversal` field-projection branch once all
  child-field fixtures use physical plans.

## W2 — Typed child filters

### Physical IR and lowering

Reuse `PhysicalPredicateExpression` and `PhysicalExtract`; do not add a
second filter language. Lower `SemanticNode.Filters` into the child set
subplan immediately after traversal scope filters and before its return.

Required-match filters use the same predicate lowering inside their `EXISTS`
subplan. This removes the last special-case relationship filter renderer.

### Renderer

Render selector predicates through the existing fixed templates for equality,
membership, text matching, date comparisons, EXISTS/MISSING, and ALL/ANY/NONE
quantifiers. Literal values remain bind variables.

### Tests and deletion

- parity matrix for every filter operator on root, child, nested child, and
  required-match routes;
- auth and dataset-generation tests with a matching row outside scope;
- live inbound and proven-outbound Explain tests;
- remove `compileTypedFilters` use from traversal and required-match helpers.

## W3 — Aggregates and derived values

### Physical IR

Use `PhysicalAggregate` and typed expressions over a set source. Support the
currently exposed semantic operations in this order:

1. count and distinct count;
2. value arrays and distinct value arrays;
3. first/representative value;
4. min/max;
5. predicate-qualified aggregates.

Define empty-set output explicitly from current GraphQL behavior before each
operation: count is `0`; value arrays are `[]`; scalar representative/min/max
are `null` unless existing tests prove otherwise.

Derived fields are expressions over previously defined typed sets or typed
aggregate values. They may not reference a textual AQL variable name.

### Renderer

Render aggregate expressions as bounded AQL subqueries over a set variable.
Use `UNIQUE`, `SORTED_UNIQUE`, `FIRST`, `MIN`, `MAX`, and `LENGTH` only for
their validated typed operations. Preserve deterministic representative sort.

### Tests and deletion

- META GDC case/file counts, values, and null/empty behavior;
- aggregation parity for no child, one child, repeated values, and scoped-out
  child documents;
- delete `compileRootAggregateExpr`, aggregate portions of
  `compileDerivedField`, and aggregate-only named-set helpers after cutover.

## W4 — Pivots

### Physical IR

Lower each `SemanticPivot` to `PhysicalPivotMap` with:

- a typed source set or root payload source;
- resource type and parsed key/value selectors;
- requested columns stored as a bound list;
- explicit collision rule and absent-column null behavior.

The pivot’s output object is a typed projection expression. Dynamic data values
must never become AQL object-key source.

### Renderer

Build the map through a fixed subquery that filters permitted columns and uses
bound column keys. Maintain the existing output naming and key order contract.

### Tests and deletion

- root and child pivots;
- sparse values, repeated keys, unknown requested column, and nested selector;
- real GDC observation pivot fixture with output-shape parity;
- delete `compileRootPivot`, `compileDerivedPivotMapLets`, and pivot-specific
  string compiler helpers once every pivot route is physical.

## W5 — Representative slices

### Physical IR

Lower `SemanticSlice` to `PhysicalSlice` over a typed child set with:

- optional typed predicate;
- explicit stable sort expression;
- bind-backed positive limit;
- nested typed object projection.

### Renderer

Render a local set subquery: filter, stable sort, limit, then return the typed
object. It must not alter the outer root window or expose an unbounded child
collection.

### Tests and deletion

- zero, one, and many child records;
- tie-breaking stability;
- filter + slice interaction and nested projected field behavior;
- delete `compileRepresentativeSlice`, `compileRootSlice`, and slice-only
  lowered helpers after physical cutover.

## W6 — Traversal sharing optimization

Do this only after W1–W5 have result parity without sharing.

1. Identify sibling child sets with identical parent set, route direction,
   edge label, scope, and traversal depth.
2. Materialize one typed broad traversal set, then typed resource-filtered
   subsets. Never share across differing target-type semantics.
3. Add a physical-plan optimization pass that rewrites only equivalent set
   subplans and records provenance/count in explain output.
4. Require parity and Explain cost improvement or neutrality; otherwise retain
   the unshared physical plan.

Delete generic compatibility named-set sharing only after this pass owns every
currently supported sharing case.

## W7 — Cutover and deletion

### Per-feature gate

A feature family may leave compatibility only when all are true:

1. semantic-to-physical lowering has deterministic plan goldens;
2. physical and compatibility execution have row/column/value parity on META;
3. physical AQL has scoped root and traversal index Explain coverage;
4. no runtime request for that family calls `compileLowered`.

### Final deletion PR

After W1–W6 pass, delete in one focused change:

- `Compile`, `Lower`, `lowerSemanticBuilder`, and `compileLowered`;
- `Builder.Sets`, `NamedSet`, lowered derived-field types, and old AQL helper
  methods that no longer have physical consumers;
- compatibility-only conformance fixtures, replacing them with physical-plan
  and execution-parity fixtures.

At that point `CompileRequest` is reduced to:

```go
semantic := BuildSemanticPlan(builder)
physical := BuildPhysicalPlan(semantic)
return RenderPhysicalPlan(physical)
```

## Work ownership and order

| Wave | Work | Shared-file owner |
| --- | --- | --- |
| 1 | W1 child sets and W2 predicates | physical lowerer owner |
| 2 | W3 aggregates and W4 pivots | physical renderer owner |
| 3 | W5 slices | physical renderer owner |
| 4 | W6 sharing + benchmark/Explain | optimizer owner |
| 5 | W7 deletion | compiler core owner |

Only the owner for a wave edits `physical_plan.go`, `physical_render.go`, and
`compile.go`. Other workers contribute isolated feature tests and fixtures.
