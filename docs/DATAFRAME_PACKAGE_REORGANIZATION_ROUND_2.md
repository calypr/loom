# Dataframe package reorganization — round 2

## Decision

The first pass solved the root-level monolith: `internal/dataframe` is now a
small compatibility facade, and runtime, templates, and errors have real
homes. The remaining structural problem is that `internal/dataframe/compiler`
is a 7.8k-line package containing five separate layers:

1. dataframe request language and FHIR selection rules;
2. logical/semantic planning;
3. physical IR and its safety invariants;
4. FHIR graph lowering and physical optimization; and
5. AQL rendering.

Those layers should become packages. The target is not many tiny packages: it
is a compiler whose directory layout mirrors its pipeline and makes dependency
direction enforceable.

This is a behavior-preserving reorganization. It must use the existing
`fhirschema` metadata, generated `fhirstructs`, graph storage contracts, and
current AQL parity fixtures. No work package invents a FHIR relationship,
storage direction, or selector rule.

## Survey of the current code

The current `internal/dataframe` subtree has about 10.5k production lines and
9.8k test lines. `compiler` owns 7.8k production lines and 3.35k unit-test
lines. Its largest files demonstrate real, distinct responsibilities:

| Current file(s) | Approx. LOC | Actual responsibility | Target owner |
| --- | ---: | --- | --- |
| `builder_types.go`, `grain.go`, `filter*.go`, `selectors.go`, `relationship_match.go` | 0.8k | public request AST, selection/filter/grain contracts | `spec` |
| `semantic_plan.go`, `semantic_validation.go`, `selection_semantics.go` | 0.75k | schema-backed logical dataframe meaning | `semantic` |
| `physical_plan.go`, `physical_helpers.go`, `physical_scope.go` | 1.8k | typed physical IR, clone and scope validation | `compiler/ir` |
| `physical_cost.go`, `physical_diagnostics.go` | 0.55k | optimizer policy/report and explainable physical facts | `compiler/ir` |
| `generic_physical_plan.go`, `physical_lowering.go`, `physical_required_match.go`, `storage_route.go` | 1.25k | FHIR graph route selection and semantic-to-IR lowering | `compiler/lower` |
| `physical_optimize.go`, `physical_prefix.go`, `optimizer.go` | 0.62k | semantics-preserving IR rewrites | `compiler/optimize` |
| `physical_render.go`, `selector_render.go`, `filter_literal.go` | 1.74k | typed IR to parameterized AQL | `compiler/render/aql` |
| `compile.go`, `physical_execution.go` | 0.19k | pipeline orchestration and `CompiledQuery` | `compiler` facade |

There is also an avoidable API-barrel problem. Root `dataframe/api.go` imports
only `runtime`; `runtime/api.go` then re-exports nearly all compiler and error
types/functions. Runtime is not the owner of the request language or compiler
IR, so this makes navigation misleading and risks runtime becoming another
mega-package.

The former experiment/tournament/live-gate tests were research harnesses, not
normal package unit tests. They obscured production ownership and have now
been retired. Durable conclusions are summarized in
[`COMPILER_PERFORMANCE.md`](COMPILER_PERFORMANCE.md).

## Target layout

```text
internal/dataframe/
  doc.go                         # architecture map and facade contract
  api.go                         # root compatibility aliases only
  errors/                        # existing structured user-error taxonomy
  template/                      # existing guided dataframe templates
  spec/                          # request AST, selector/filter/grain contracts
  semantic/                      # request -> backend-independent FHIR plan
  compiler/
    api.go                       # compiler compatibility surface + orchestration
    compile.go                   # semantic -> lower -> optimize -> render
    ir/                          # typed physical plan, validation, diagnostics
    lower/                       # FHIR graph/storage-route lowering
    optimize/                    # IR-only rewrites and policy decisions
    render/aql/                  # IR -> parameterized AQL
  runtime/                       # catalog preparation, scope, execution, profile

conformance/compiler/
  experiments/                   # ablations/tournaments; explicit environment gates
  integration/                   # live Arango parity/Explain coverage
```

`compiler` stays a package because it is the useful public programming model
for the profile CLI, conformance suite, and root facade. Its children are
implementation layers. All physical types live in `compiler/ir`, not in
`lower`, `optimize`, or `render`, so no implementation layer owns another
layer's data model.

## Dependency rules

```text
fhirschema, fhirstructs, authscope
              |
              v
            spec
              |
              v
           semantic
              |
              v
        compiler/ir <---- compiler/optimize
              ^                 |
              |                 v
       compiler/lower ----> compiler/render/aql
              \                 /
               \               /
                v             v
                   compiler
                      |
                      v
                   runtime
                      |
                      v
              dataframe compatibility facade
```

Rules that must be mechanically enforced:

- `spec` imports schema metadata and `authscope` only; it imports neither
  runtime, catalog, Arango, semantic, nor compiler implementation packages.
- `semantic` imports `spec` and `fhirschema`; it does not know collection
  names, bind variables, AQL, or `Physical*` types.
- `compiler/ir` imports `spec` only where the typed IR embeds a selector. It
  must not import `lower`, `optimize`, `render`, runtime, catalog, or Arango.
- `compiler/lower` imports `semantic`, `spec`, `compiler/ir`, and generated
  schema metadata. It is the only layer that may call `ResolveStorageRoute` or
  select endpoint versus native graph traversal.
- `compiler/optimize` imports only `compiler/ir`; it never uses rendered AQL,
  FHIR resource-name special cases, catalog data, or Arango clients.
- `compiler/render/aql` imports `compiler/ir` and `spec`; it serializes an
  already-valid plan and never chooses a FHIR route or optimizer rule.
- `runtime` imports compiler, errors, catalog/dataset/authscope/Arango. No
  compiler child may import runtime.
- The root `dataframe` package imports canonical owners directly; it must not
  use `runtime` as a transitive alias barrel.

## Work packages

### P0 — Freeze the behavior and public surface

**Owner:** one coordinator. **No source moves.**

1. Record current public root symbols used by `graphqlapi`, `internal/httpapi`,
   commands, and `conformance/compiler`.
2. Capture hashes for the canonical GDC fixture's rendered AQL, bind variables,
   columns, result rows, and optimization diagnostics.
3. Run and retain baseline outputs:

   ```bash
   GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test -short ./internal/dataframe/... ./conformance/compiler
   GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./graphqlapi/dataframe ./graphqlapi ./internal/httpapi
   ```

4. Add a temporary package-boundary test script that fails if a child of
   `internal/dataframe` imports its parent.
5. Treat existing untracked benchmark artifacts and the previous reorg plan as
   user work: do not rename, delete, or overwrite them.

**Acceptance:** a move can be shown to preserve a known rendered query and
public API; no package split is allowed to mask a parity failure.

### P1 — Remove the transitive runtime API barrel

**Owner:** one worker. **Must complete before parallel package extraction.**

1. Split `compiler/builder_types.go` conceptually before moving code:
   `Builder`, traversals, selection types, filters, and grain are compiler
   inputs; `RunRequest`, `Result`, `QueryDiagnostics`, and `StreamResult` are
   runtime results.
2. Make `runtime/api.go` private to the runtime package's own types, then
   delete it once all runtime files use explicit `compiler.*` and
   `dataframeerrors.*` references.
3. Update root `dataframe/api.go` to alias compiler, runtime, and errors from
   their canonical packages directly. Preserve every current root symbol.
4. Keep aliases—not wrapper structs—for `Builder`, `Service`, `CompiledQuery`,
   and all physical types. Methods and JSON fixtures must remain unchanged.

**Acceptance:** direct compiler imports remain limited to runtime and root
facade; `runtime` no longer exports the compiler's entire surface.

### P2 — Extract `spec`

**Owner:** one worker after P1. May run in parallel with P6 inventory only.

Create `internal/dataframe/spec` and move:

- request AST from `builder_types.go` (`Builder`, `TraversalStep`,
  `FieldSelect`, `PivotSelect`, `AggregateSelect`, `RepresentativeSlice`);
- `grain.go`, `relationship_match.go`, `filter.go`, `filter_semantics.go`, and
  `selectors.go`;
- the schema-only selector/filter unit tests.

Do not move `filter_literal.go` or `selector_render.go`: their job is AQL
emission and they belong in `compiler/render/aql`.

Expose constructor-free value types and validation helpers only. `spec` must
not absorb service result types, catalog-populated-field checks, or AQL text.
Add compiler aliases in `compiler/api.go`, then preserve root aliases in
`dataframe/api.go`.

**Acceptance:** `go list -deps ./internal/dataframe/spec` contains no runtime,
catalog, Arango, semantic, lower, optimize, or render package.

### P3 — Extract `semantic`

**Owner:** one worker after P2. May not overlap P4-P5.

Create `internal/dataframe/semantic` and move:

- `semantic_plan.go`;
- `semantic_validation.go`;
- `selection_semantics.go`;
- the semantic plan/validation/selection tests.

`semantic.Plan` owns `SemanticNode`, fields, pivots, aggregates, slices, row
identity, aliases, match modes, and schema validation. It must contain no AQL
variable names, collection binds, endpoint fields, or Arango explain data.
Move `selectorExecutionMode` out of `selection_semantics.go`: selector mode is
a physical rendering/lowering decision, not a semantic one.

**Acceptance:** semantic package tests run against `fhirschema` and `spec`
without importing any `compiler/*` implementation package.

### P4 — Establish `compiler/ir` and the physical proof boundary

**Owner:** one coordinator. **Serial; it changes shared type ownership.**

Create `internal/dataframe/compiler/ir`. Move and split the physical model:

- `physical_plan.go` -> `plan.go`, `expression.go`, `predicate.go`,
  `validate.go`;
- clone functions from `physical_helpers.go` -> `clone.go`;
- `physical_cost.go` -> `policy.go` and `policy_report.go`;
- `physical_diagnostics.go` -> `diagnostics.go`;
- IR-only portions of `physical_scope.go` -> `validate_scope.go`.

Before moving `physical_prefix.go`, extract the generic navigation and exact
scope proofs currently shared with renderer (`validateGenericNavigationTraversal`,
`validateGenericNavigationScopeBlock`, and their dependencies) into
`ir/validate_navigation.go`. This deliberately removes the current wrong-way
dependency where optimizer analysis depends on helpers defined in renderer.

Keep these invariants in IR:

- all `Physical*` model types and `Plan.Validate`;
- bind/collection validation, variable definition/use rules, cloning, and
  generic project/generation/auth scope proof;
- optimization policy/report data, but not environment-variable policy
  loading; and
- renderer-independent diagnostics and traversal-prefix decomposition input.

**Acceptance:** `compiler/ir` has no FHIR route resolution and no AQL string
construction. Both lowering and renderer can consume the same validated plan.

### P5 — Extract `lower`, `optimize`, and `render/aql`

**Owner:** serial coordinator for all three. Do not parallelize these moves.

#### P5a — `compiler/lower`

Move `generic_physical_plan.go`, `physical_lowering.go`,
`physical_required_match.go`, `storage_route.go`, compiler-specific
`auth_scope.go`, `dataset_generation.go`, and lowering helpers from
`physical_helpers.go`.

Split into `lower.go`, `route.go`, `scope.go`, `required_match.go`,
`children.go`, and `rich_shapes.go`. The package accepts `semantic.Plan` and
returns `ir.Plan`. It remains the sole owner of FHIR storage direction, edge
endpoint contracts, project/generation/auth scope insertion, required
semi-joins, and compact/prepared child-set construction.

#### P5b — `compiler/optimize`

Move `physical_optimize.go`, `physical_prefix.go`, and `optimizer.go`.
Expose `Apply(plan ir.Plan, policy ir.OptimizationPolicy)`. Environment-based
default-policy construction belongs here or in compiler facade, but its result
must be an `ir.OptimizationPolicy`. All alpha-renaming, traversal-prefix
sharing, and plan rewrites stay here.

#### P5c — `compiler/render/aql`

Move `physical_render.go`, `selector_render.go`, and `filter_literal.go`.
Split the 1.6k-line renderer by behavior: `render.go`, `root.go`,
`traversal.go`, `set.go`, `expression.go`, `aggregate.go`, `pivot.go`,
`slice.go`, `selector.go`, and `validate.go`.

`Render(ir.Plan)` returns an immutable rendered-query value. It may allocate
internal bind names and prune runtime binds, but it cannot alter the IR or
decide routes/optimization.

**Acceptance:** lower, optimize, and render have only one-way dependencies:
they all consume `ir`; none imports another implementation sibling.

### P6 — Rebuild the small compiler facade

**Owner:** coordinator after P5.

Keep `internal/dataframe/compiler` as the stable entry point, but reduce its
production implementation to:

- `CompiledQuery` and its output metadata;
- `CompileRequest` / `CompileRequestWithPolicy` orchestration;
- `DefaultPhysicalOptimizationPolicy` forwarding;
- type/function aliases intentionally preserved for root callers and
  conformance; and
- integration tests that assert the complete semantic -> lower -> optimize ->
  render pipeline.

`physical_execution.go` should become `compiler/output.go`: it adds the
root-window operation, invokes renderer, and derives user-visible columns and
diagnostics. It must not become a second renderer.

**Acceptance:** `compiler` imports `spec`, `semantic`, `ir`, `lower`,
`optimize`, and `render/aql`; its own non-alias production code should be
under roughly 500 lines.

### P7 — Finish runtime organization without over-splitting it

**Owner:** may run alongside P6 only after compiler aliases are stable.

Runtime is only ~1.4k production lines, so retain one `runtime` package but
make its file names reflect lifecycle:

- `types.go`: `RunRequest`, `Result`, stream/diagnostic output;
- `service.go`: dependency injection and public Run/Stream;
- `prepare.go`: active generation and authorization resolution;
- `catalog_validation.go`: discovered field/reference checks;
- `pivot_materialization.go`: flattening result pivots;
- `cursor.go`: query execution;
- `observability.go`: Explain, Profile, and timing helpers.

Move no compiler semantics into runtime. Runtime can validate what is
populated in a loaded dataset; it cannot reimplement schema/semantic or AQL
rules.

**Acceptance:** a developer can locate a service request's preparation,
catalog validation, cursor execution, and profiling without opening an API
barrel.

### P8 — Retire research and live tournament tests

**Owner:** coordinator after P6/P7. No source move is required.

Delete the historical `*_tournament_test.go`, `*_experiment_test.go`, and
strategy/live-gate harnesses once their durable conclusions have been captured
in [`COMPILER_PERFORMANCE.md`](COMPILER_PERFORMANCE.md). Keep stable compiler
unit tests and generic opt-in Explain/result-parity tests that exercise the
production path. Delete generated AQL/profile artifacts with the harnesses;
they are not fixtures and cannot act as regression tests.

**Acceptance:** normal `go test ./internal/dataframe/...` is an ownership
suite, and performance work is represented by production code plus the concise
performance note rather than stale candidate rewrites.

### P9 — Enforce and document the architecture

1. Update `docs/DEVELOPER_ARCHITECTURE.md` with the target graph and a
   “where does this change go?” table.
2. Add a dependency-check test/script for the rules above.
3. Add focused CI commands for spec, semantic, IR, lower, optimize, renderer,
   compiler facade, runtime, and root compatibility.
4. Search for stale imports and require all public callers to compile through
   the root facade before removing compatibility aliases in a separate change.

## Execution order and parallelism

| Phase | Work | Parallelism |
| --- | --- | --- |
| 0 | P0 baseline | coordinator only |
| 1 | P1 API-barrel removal | coordinator only |
| 2 | P2 spec | P8 test inventory may run in parallel |
| 3 | P3 semantic | no physical move yet |
| 4 | P4 IR | coordinator only |
| 5 | P5 lower -> optimize -> render | strictly serial |
| 6 | P6 compiler facade and P7 runtime | may run in parallel after stable aliases |
| 7 | P8 test retirement, P9 docs/enforcement | may run in parallel after P6/P7 |

The physical packages must not be delegated to separate workers at the same
time: they share the IR, scope proof, and public aliases. Parallel work there
will create import cycles or silently weaken the authorization/generation
contract.

## Global acceptance gates

After every package move:

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test -short ./internal/dataframe/... ./conformance/compiler
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./graphqlapi/dataframe ./graphqlapi ./internal/httpapi
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./...
git diff --check
```

For any physical move, additionally prove the canonical GDC dataframe keeps
the same rendered AQL/bind/result hashes and run the existing live Arango
Explain/result-parity gate when its container is available. A package move may
change imports and file paths; it may not change FHIR schema behavior, graph
scope, authorization, dataset-generation isolation, or query results.

## Implementation status

The production extraction is now in place. `spec`, `semantic`,
`compiler/ir`, `compiler/lower`, `compiler/optimize`, and
`compiler/render/aql` are separate packages with one-way source dependencies.
The root `dataframe` package is a compatibility facade, and compiler
orchestration remains in the small `compiler` package. Runtime result types
are separate from request/spec types. `runtime/api.go` remains as a source-
compatibility barrel for direct runtime importers; new code should import the
canonical owner.

The package-boundary checks are executable with:

```bash
make dataframe-boundaries
```

The research/tournament tests and generated benchmark corpus have been
retired. Their durable strategy findings are preserved in
[`COMPILER_PERFORMANCE.md`](COMPILER_PERFORMANCE.md), while production package
tests and the conformance compiler suite remain the correctness suite.
