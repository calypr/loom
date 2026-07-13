# Dataframe package reorganization plan

## Objective

Turn `internal/dataframe` from the repository's catch-all package into a thin,
backwards-compatible façade over small packages with one-way dependencies. The
move is organizational: rendered AQL, request semantics, authorization,
generation isolation, result shape, and public callers must retain their
current behavior.

The goal is not to make every package tiny. The physical compiler is a real,
cohesive subsystem. The goal is that a new developer can find request types,
semantic meaning, physical AQL lowering, runtime execution, templates, and
errors without reading one 17.5k-line package.

## Current evidence

`internal/dataframe` contains 89 Go files and roughly 17.5k code lines. The
largest production responsibilities are currently interleaved:

| Current files | Lines | Actual responsibility |
| --- | ---: | --- |
| `physical_render.go` | 1,539 | AQL serialization and renderer safety checks |
| `physical_plan.go` | 1,137 | typed physical IR and IR validation |
| `generic_physical_plan.go` | 790 | semantic-to-physical lowering |
| `semantic_plan.go` | 397 | request-to-semantic-plan construction |
| `physical_optimize.go`, `physical_cost.go`, `physical_prefix.go`, `physical_diagnostics.go` | 963 | physical optimization policy, decisions, and diagnostics |
| `service.go`, `execution.go`, `validation.go`, `pivots.go`, auth/generation files | 1,100+ | catalog-aware request preparation and execution |
| `errors.go` | 320 | transport-neutral product error contract |
| experiment/tournament tests | 4,000+ | AQL research evidence mixed into the core package |

The root package is imported by the GraphQL service, HTTP error mapper,
server command, profile CLI, and compiler conformance suite. Those callers
need stable names such as `Builder`, `CompileRequest`, `Service`,
`CompiledQuery`, and `ErrorCode`; they do **not** need access to physical IR
implementation files.

## Target layout

```text
internal/dataframe/
  doc.go                         # package contract and architecture map
  api.go                         # compatibility aliases/wrappers only
  template/                      # already separate; retain
  spec/                          # request, selection, filter, grain contracts
  semantic/                      # backend-independent FHIR dataframe meaning
  physical/
    plan/                        # physical IR, validation, shared diagnostics
    lower/                       # semantic -> physical FHIR graph lowering
    optimize/                    # semantics-preserving physical rewrites
    render/                      # physical IR -> parameterized AQL
  compiler/                      # orchestration and CompiledQuery output
  runtime/                       # catalog/scope/generation preparation, Run/Stream/Validate
  errors/                        # stable user-facing error taxonomy

conformance/compiler/experiments/ # tournament and ablation tests that use public API only
```

The root `dataframe` package remains intentionally small: documented stable
entrypoints, type aliases, constant aliases, and thin wrappers. It must not
contain a child package's implementation. Existing imports therefore continue
to work while callers are migrated only when doing so clarifies ownership.

## Dependency rule

Child packages must never import `internal/dataframe` (their parent). The
allowed direction is:

```text
fhirschema, fhirstructs, authscope, catalog, dataset, store/arango
                         │
                         ▼
                       spec
                         │
                         ▼
                     semantic
                         │
                         ▼
                 physical/plan
                   ▲      ▲      ▲
                   │      │      │
                lower  optimize render
                   \      |      /
                    \     ▼     /
                     └ compiler ┘
                         │
                         ▼
                      runtime
                         │
                         ▼
                 dataframe façade
```

`errors` is independent and may be imported by `runtime` and transport
adapters. `template` remains independent of compiler/runtime and may import
only generated schema metadata plus its own types.

This direction prevents the two most likely Go-cycle failures:

1. runtime/catalog code importing compiler code which imports the root
   `dataframe` service; and
2. physical lowering importing a semantic package that imports physical types
   merely to express a storage direction.

Storage routes belong in `physical/lower`, because physical direction is a
stored-edge decision rather than semantic FHIR meaning.

## Public API compatibility contract

Before moving production files, freeze the root surface with an API inventory.
At minimum preserve:

- `Builder`, request selection/filter/traversal types, selector helpers, row
  grain/identity types, and typed filter validation;
- `CompileRequest`, `CompileRequestWithPolicy`, `CompiledQuery`, optimization
  policy/rule/diagnostic types;
- `Service`, `ServiceConfig`, `RunRequest`, `Result`, `StreamResult`,
  `ValidateRequest`, `ValidationResult`, and `QueryDiagnostics`;
- `ExplainCompiledQuery`, `ProfileCompiledQuery`, and `ExecuteQueryRows`;
- the existing `ErrorCode`, `UserError`, `Error`, and constructors.

Use Go type aliases in `api.go` for types whose methods must remain visible:

```go
type Builder = spec.Builder
type Service = runtime.Service
type CompiledQuery = compiler.CompiledQuery
type ErrorCode = dataframeerrors.Code
```

Use forwarding functions for constructors and functions. Preserve constant
names through constant aliases. Do not use wrapper structs for `Builder`,
`Service`, or `CompiledQuery`: wrappers would silently break assignment,
methods, JSON fixtures, and conformance callers.

## Work packages

### WP0 — Baseline, ownership, and failure gate

**Purpose:** make the reorganization measurable before moving source.

1. Record `go list` importers of `internal/dataframe`, exported API via
   `go doc`, file LOC, and the existing compiler conformance result hash.
2. Add this plan to the developer architecture index and create a short
   package-owner table in `internal/dataframe/doc.go`.
3. Capture the current rendered GDC AQL hash and result-parity fixtures.
4. Resolve or explicitly quarantine the current four failing root-package
   assertions before extracting physical code:
   - `TestCompileRequestUsesPhysicalExecutionForNavigationOnlyGenericPlan`;
   - `TestRenderPhysicalPlanGenericNavigation`;
   - `TestRenderPhysicalPlanTraversalSetsPreserveRootRowGrain`;
   - `TestIdentityDedupCandidateBuildsActualGDC`.

   They currently assert old native-traversal/projection text while the active
   renderer emits endpoint lowering. Reconcile them as a separate behavior
   verification change, not as a side effect of the package move.
5. Establish a gate after every package move:

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test -short ./internal/dataframe/... ./conformance/compiler
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./graphqlapi/dataframe ./graphqlapi ./internal/httpapi
git diff --check
```

**Acceptance:** a green baseline or a documented, isolated baseline exception
exists before any mechanical move. No behavior test is weakened to make a
move appear safe.

### WP1 — Extract the request/specification package

**New package:** `internal/dataframe/spec`.

**Move as one cohesive unit:**

- `builder_types.go`;
- `grain.go`;
- `relationship_match.go`;
- `selectors.go`;
- `filter.go` and `filter_semantics.go`.

**Keep out of this package:** `filter_literal.go` and `selector_render.go`.
They turn validated values/selectors into AQL expressions and therefore belong
with the physical renderer.

**Implementation steps:**

1. Move all request AST types, row-grain identity, selector aliases/parsing,
   traversal match mode, filter values, and schema-only filter validation.
2. Replace direct root-package references with `spec.*` in semantic and
   physical code.
3. Add aliases/constants/functions in root `api.go` before updating external
   callers. Keep existing JSON fixture shape unchanged.
4. Move the corresponding unit tests to `spec` and retain external conformance
   tests through the root façade.
5. Add a compile-only test that proves a package importing only root
   `dataframe` can still construct every public request shape.

**Acceptance:** `spec` has no catalog, dataset, Arango, AQL-rendering, or
runtime-service import. The root package has no duplicated request type.

### WP2 — Extract semantic planning

**New package:** `internal/dataframe/semantic`.

**Move:**

- `semantic_plan.go`;
- `semantic_validation.go`;
- `selection_semantics.go`;
- semantic portions of `optimizer.go` if they only describe logical rules.

**Implementation steps:**

1. Make `semantic.Plan`, node/field/pivot/aggregate/slice types, plan
   construction, graph validation, and selection normalization use `spec`.
2. Keep semantic plans free of AQL variable names, collection bind keys,
   physical directions, catalog clients, and Arango explain data.
3. Export only the semantic API needed by compiler/physical lowering. Keep
   internal traversal walk helpers private.
4. Move semantic-plan, selection-semantics, and semantic-validation tests with
   the package.
5. Retain root aliases for `SemanticPlan` and public diagnostic explanation
   types until all callers are deliberately migrated.

**Acceptance:** semantic planning can compile and test with only `spec` and
generated FHIR schema dependencies. No semantic package import reaches
`physical`, `runtime`, catalog, or Arango.

### WP3 — Split the physical compiler by responsibility

This is the largest move and must be sequential; do not parallel-edit the
physical type graph.

#### WP3a — Physical plan model

**New package:** `internal/dataframe/physical/plan`.

**Move:**

- `physical_plan.go`;
- `physical_scope.go` only where it validates generic physical operations;
- `physical_diagnostics.go`;
- `physical_cost.go`.

`plan` owns typed physical operations, their validation, policy/decision
records, and compiler diagnostics. It imports `spec`, but not lowering,
optimization, renderer, service, catalog, or Arango.

#### WP3b — Lowering

**New package:** `internal/dataframe/physical/lower`.

**Move:**

- `generic_physical_plan.go`;
- `physical_lowering.go`;
- `physical_helpers.go`;
- `physical_required_match.go`;
- `storage_route.go`;
- lowering-specific portions of `physical_scope.go`.

It imports `semantic`, `physical/plan`, `spec`, and `fhirschema`. It is the
only package allowed to select a proven storage route and endpoint-versus-native
traversal strategy.

#### WP3c — Optimization

**New package:** `internal/dataframe/physical/optimize`.

**Move:**

- `physical_optimize.go`;
- `physical_prefix.go`;
- physical portions of `optimizer.go`.

It imports `physical/plan` only. It must not render strings or call Arango.
Keep optimization policy defaults/decision reports in `plan` so they remain
available to the compiler façade and diagnostics without a cycle.

#### WP3d — Rendering

**New package:** `internal/dataframe/physical/render`.

**Move:**

- `physical_render.go`;
- `selector_render.go`;
- `filter_literal.go`.

Split the current 1,662-line renderer into files in the same `render` package:

| New file | Content |
| --- | --- |
| `render.go` | public `Render`, bind state, top-level orchestration |
| `render_root.go` | root scan/window/sort/limit operations |
| `render_traversal.go` | native and endpoint traversal sets, required matches |
| `render_expression.go` | extraction, predicates, aggregates, pivots, slices, projections |
| `render_selector.go` | selector and typed-filter expression emission |
| `validate.go` | renderer-only safety validation |

The renderer imports `physical/plan` and `spec`, returns an immutable rendered
query value, and has no catalog/runtime dependency.

**Acceptance for WP3:** the actual AQL generated for all conformance fixtures
is byte-for-byte identical unless a separately approved behavior change is in
flight; bind variables and result hashes remain equal; physical package tests
are co-located under the owning subpackage.

### WP4 — Create a compiler façade

**New package:** `internal/dataframe/compiler`.

**Move/replace:** `compile.go` and `physical_execution.go`.

1. Define `compiler.CompiledQuery` as the stable compiler output.
2. Orchestrate `semantic.Build`, `lower.Build`, `optimize.Apply`, and
   `render.Render` in exactly the current order.
3. Preserve `CompileRequest` and `CompileRequestWithPolicy` through root
   forwarding functions; retain direct root imports for conformance and CLIs
   during the migration.
4. Move compiler integration tests and generic physical execution tests here.
5. Add result-parity tests that compile through both root façade and compiler
   package during the migration, then remove the duplicate route after the
   façade is proven.

**Acceptance:** no runtime/catalog/Arango dependencies in compiler. Compiler
can be invoked by the profile CLI and conformance fixtures without constructing
a service.

### WP5 — Extract runtime preparation and execution

**New package:** `internal/dataframe/runtime`.

**Move:**

- `service.go`;
- `execution.go`;
- `validation.go` and `validation_service.go`;
- `pivots.go`;
- `active_generation.go`, `auth.go`, `auth_scope.go`, and
  `dataset_generation.go`;
- `query_runtime.go`, `explain.go`, and `profile.go`.

**Implementation steps:**

1. Replace direct compiler calls with `compiler.CompileRequest`.
2. Retain dependency injection for catalog discovery and row execution so
   runtime tests remain database-free.
3. Keep Arango access isolated to `ExecuteQueryRows`, Explain, and Profile;
   the rest of runtime must remain testable from injected functions.
4. Move catalog-aware validation and pivot expansion here, because they depend
   on observed populated fields rather than only FHIR schema semantics.
5. Move service, execution, validation, active-generation, auth-scope, pivot,
   and Explain/Profile tests with this package.
6. Re-export `Service`, `ServiceConfig`, execution options, and validation
   result types from root aliases/wrappers.

**Acceptance:** runtime depends downward on compiler/spec/errors and external
infrastructure packages, but compiler/semantic/physical never import runtime.

### WP6 — Extract error taxonomy and trim the root façade

**New package:** `internal/dataframe/errors`.

**Move:** `errors.go` and its tests.

1. Preserve root aliases and constructors so `graphqlapi/errors.go` and
   `internal/httpapi/errors.go` continue compiling unchanged.
2. Update transport adapters to import `dataframe/errors` only after the root
   compatibility suite has passed; this is optional, not required for the
   initial move.
3. Leave `internal/dataframe/template` intact; it is already a meaningful
   standalone package and must not import runtime/compiler.
4. Reduce root production source to `doc.go` and `api.go` (target: under 400
   lines total, excluding compatibility comments).

**Acceptance:** `internal/dataframe` contains no physical rendering, storage,
catalog, auth, generation, or error implementation.

### WP7 — Retire research tests and benchmark artifacts

The package was inflated by more than 4k lines of ablation/tournament tests.
Those harnesses were useful during compiler development but are not durable
correctness tests.

1. Delete historical endpoint, selector, compact-projection, identity-order,
   pivot, and materialization tournament/experiment harnesses.
2. Delete generated candidate AQL, profile JSON, and decision artifacts with
   the harnesses.
3. Preserve stable compiler unit tests and generic opt-in Arango
   Explain/result-parity tests beside the owning package.
4. Capture reusable conclusions in `docs/COMPILER_PERFORMANCE.md` rather than
   preserving candidate rewrites as executable tests.

**Acceptance:** production package directories contain ownership tests only,
and performance work is represented by production code, focused profiling,
and the concise performance note.

### WP8 — Enforce the new architecture

1. Update `docs/DEVELOPER_ARCHITECTURE.md` with the target dependency graph,
   public façade policy, and “where does this change belong?” table.
2. Add a lightweight architecture test/script that rejects child packages
   importing their parent `internal/dataframe` package.
3. Add package-scoped CI commands for `spec`, `semantic`, physical packages,
   compiler, runtime, template, and root compatibility façade.
4. Add a review checklist: new AQL text goes to `physical/render`; FHIR graph
   route decisions go to `physical/lower`; request/canonical semantics go to
   `spec` or `semantic`; catalog/scope/streaming work goes to `runtime`.
5. Remove stale compatibility aliases only in a separately approved major
   internal API cleanup after every direct importer has migrated.

## Parallelism plan

| Lane | May run in parallel with | Must wait for |
| --- | --- | --- |
| WP0 baseline and importer inventory | documentation only | nothing |
| WP1 spec | error extraction design | WP0 API inventory |
| WP2 semantic | root façade scaffolding | WP1 |
| WP3 physical | none within physical | WP2 |
| WP4 compiler façade | renderer test organization | WP3 |
| WP5 runtime | WP7 research-test inventory | WP4 |
| WP6 errors | WP7 inventory | root façade scaffolding |
| WP7 test retirement | WP5 only for runtime tests | each owning package move |
| WP8 enforcement/docs | final verification | WP1-WP7 |

Do not assign separate workers to `physical/plan`, lowering, optimizer, and
renderer simultaneously. Their shared IR is the compiler's hottest and most
volatile boundary; serial ownership avoids accidental import cycles and
semantic drift.

## Completion criteria

The reorganization is complete only when:

- root `internal/dataframe` is a documented façade under 400 production LOC;
- all production implementation belongs to named child packages;
- no child imports its parent package;
- existing GraphQL, HTTP, CLI, and conformance imports compile through the
  root compatibility surface;
- compiler fixture AQL/bind/result parity and live Explain checks are preserved;
- runtime scope/generation/authorization tests remain green;
- historical tournament harnesses are retired; durable performance findings
  live in [`COMPILER_PERFORMANCE.md`](COMPILER_PERFORMANCE.md);
- the developer architecture document explains where the next feature belongs.

## One-shot implementation status

The initial reorganization pass is complete with the following concrete
boundaries:

- `internal/dataframe/compiler` now owns the pure request/semantic/physical
  compiler and its compiler tests;
- `internal/dataframe/runtime` owns catalog-aware preparation, authorization,
  generation pinning, validation, execution, streaming, Explain, and Profile;
- `internal/dataframe/errors` owns the structured error contract;
- `internal/dataframe/template` remains the independent guided-template
  package;
- `internal/dataframe/api.go` is the compatibility façade used by existing
  GraphQL, CLI, HTTP, and conformance imports.

The follow-up split in
[`DATAFRAME_PACKAGE_REORGANIZATION_ROUND_2.md`](DATAFRAME_PACKAGE_REORGANIZATION_ROUND_2.md)
has now established the IR visibility boundary and moved production code into
`spec`, `semantic`, `compiler/ir`, `compiler/lower`,
`compiler/optimize`, and `compiler/render/aql`. The compiler facade remains the
stable orchestration/API package. Research tests are intentionally still in
their historical locations until the separate conformance-test relocation is
approved and its environment gates are preserved.
