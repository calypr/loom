# Formal Gap Analysis: From Prototype to Development Service

## 1. Purpose

This document is the implementation plan for moving Loom from a working
dataframe prototype to a deployable development service that can:

1. ingest an unfamiliar FHIR dataset into ArangoDB
2. analyze what the dataset actually contains
3. ask a non-technical user a small number of meaningful questions
4. create a versioned dataframe recipe
5. validate and explain the recipe
6. compile the recipe into performant, authorization-safe AQL
7. preview the result
8. export flat NDJSON or CSV
9. optionally load the same row stream into Elasticsearch

This is a plan, not a claim that those capabilities already exist.

The compiler is the first implementation priority. The detailed authoritative
program for FHIR semantic lowering, filters, grains, pivots, aggregates,
physical AQL generation, optimizer passes, and Arango performance is
[`COMPILER_FIRST_PLAN.md`](COMPILER_FIRST_PLAN.md). The service-oriented gaps
below are downstream work and must not distort or precede the compiler core.

The intended implementer is an engineering agent working directly in this
repository. Every gap therefore includes concrete ownership, implementation
steps, tests, dependencies, and completion criteria.

## 2. Product Boundary

### 2.1 What "any FHIR" should mean

The first production contract should be precise:

> Loom can losslessly ingest any valid resource type represented by its active
> FHIR graph schema, preserve unknown profile extensions in the raw payload,
> discover populated fields and references, and offer dataframe recipes for
> query shapes supported by its planner.

It should not initially mean:

- every historical and future FHIR release without selecting a schema package
- arbitrary cross-resource graph queries
- automatic clinical interpretation of unknown extensions
- every possible FHIRPath function
- automatic production of a useful dataframe when the source data has no
  stable identifiers or usable relationships

FHIR version, graph schema, semantic vocabulary, and planner capability must be
visible metadata. Unsupported query shapes must be reported honestly.

### 2.2 Product architecture

The target flow is:

```text
FHIR input
  -> validated project load
  -> dataset manifest and analysis snapshot
  -> recipe templates filtered by observed data and planner support
  -> short guided conversation
  -> normalized versioned recipe
  -> validation and cardinality explanation
  -> logical plan
  -> optimized AQL
  -> preview row stream or asynchronous export row stream
  -> NDJSON / CSV / Elasticsearch
```

The frontend must not build AQL, infer graph safety, calculate authorization
scope, or decide whether a traversal is supported.

## 3. Current Baseline

The repository already contains valuable production-shaped components:

- the 14-resource development dataset under `META/` and smaller `META_SMALL/`
- the active graph schema at `schemas/graph-fhir.json`
- generated Go FHIR structs, validators, and edge extractors in `internal/fhir`
- generated FHIR field/traversal metadata in `internal/fhirschema/generated.go`
- existing generation commands in `cmd/generate`, `Makefile`, and `gqlgen.yml`
- NDJSON and gzip discovery and scanning in `internal/ingest`
- generated and generic FHIR row builders
- one Arango collection per discovered resource type
- a shared `fhir_edge` graph
- load-time field profiling in `fhir_field_catalog`
- project and auth-resource-path scoping
- populated field and relationship discovery
- discovery caching with project invalidation
- generated FHIR schema metadata
- mechanically derived friendly `fieldRef` support in `dataframebuilder`
- explicit optimized traversal semantics in `dataframe/traversal_rules.go`
- GraphQL introspection and dataframe execution
- a logical request, lowering planner, optimized AQL compiler, and query runner
- a browser builder suitable for development diagnostics
- unit and integration tests for important compiler paths

All implementation packages in this plan must extend those owners. The plan
does not authorize a parallel FHIR object model, handwritten copies of generated
validators/extractors, a replacement graph schema, or manual edits to
gqlgen-generated files. Use `META/` for baseline characterization and add
synthetic conformance fixtures only where the existing sample lacks a required
case.

The current limiting facts are:

- generated ingestion rejects resource types outside its generated switch
- generic ingestion still requires a class in the configured graph schema
- HTTP import is synchronous and accepts one staged resource file
- the field catalog stores bounded distinct samples but not a complete dataset
  manifest, relationship coverage, fanout, or value frequencies
- product-level recipes and templates do not exist
- planner lowering requires a Patient root
- planner traversal support is a hardcoded list of recognized tuples
- simple structurally valid requests may be rejected if they do not match the
  optimized lowering family
- input supports only a narrow filter/predicate surface
- preview applies a limit but accumulates all returned rows in memory
- cursor fields exist in the GraphQL input but are not an implemented paging
  contract
- export handles, durable jobs, files, and Elasticsearch delivery do not exist
- `/healthz` is liveness only; readiness and dependency diagnostics are absent

## 4. Delivery Strategy

Implement the compiler program first, then build thin product and delivery
layers around it. Do not begin with a larger frontend.
For multi-worker execution, use
[`TERRA_ULTRA_EXECUTION_PLAN.md`](TERRA_ULTRA_EXECUTION_PLAN.md), which splits
the durable job work into an early substrate and later export recovery, defines
contract freezes, and prevents unsafe parallel edits to shared packages.

| Milestone | Outcome | Work |
| --- | --- | --- |
| M0 | Compiler oracle and typed contracts | CP0-CP2 |
| M1 | FHIR grain, filters, pivots, and correct generic lowering | CP3-CP6 |
| M2 | Typed AQL generation and optimizer | CP7-CP8 |
| M3 | Arango evidence and compiler release gate | CP9 |
| M4 | Thin recipe/capability/preview/export layer | reduced G6-G14 |
| M5 | Development-service durability and delivery | reduced G2-G5, G15-G20 |

Each milestone must leave the repository testable and internally consistent.
Do not merge a new public API before its service and contract tests exist.

---

# Gap 1: No Executable Product Use-Case Contract

## Current state

The repository has examples and a working browser builder, but it does not have
a machine-readable set of user conversations defining what the product must
successfully create.

Without those fixtures, planner expansion can become an unbounded attempt to
support all graph shapes, and frontend work can expose capabilities the backend
does not implement.

## Target state

The repository contains a versioned conformance corpus covering at least these
recipe families:

- patient cohort
- specimen inventory
- file manifest
- diagnoses
- labs/observations
- study enrollment

Each fixture declares the user's words, normalized intent, required dataset
features, expected recipe, expected row grain, output schema, and support state.

## Implementation plan

1. Create `conformance/recipes/`.
2. Define a JSON fixture schema in `conformance/recipes/schema.json`.
3. Give every fixture these fields:
   - `id`
   - `description`
   - `conversation`
   - `projectFixture`
   - `requiredResources`
   - `requiredRelationships`
   - `expectedRecipe`
   - `expectedColumns`
   - `expectedGrain`
   - `expectedWarnings`
   - `expectedSupportState`
4. Add positive fixtures for all six recipe families.
5. Add ambiguity fixtures, for example "files by patient" without an explicit
   row grain.
6. Add unsupported fixtures, including missing relationships and unknown
   clinical concepts.
7. Reuse `META/` for baseline and representative end-to-end cases. Add small
   NDJSON datasets under `conformance/data/<fixture>/` only for isolated,
   missing, ambiguous, or failure cases that `META/` cannot express.
8. Create `conformance/run_conformance.py` as an orchestrator that can invoke Go
   tests or a running server and produce JSON results. Keep correctness logic in
   Go tests; the script should coordinate rather than reimplement Loom.
9. Add `make conformance`.
10. Document how to add a fixture.

## Tests

- JSON Schema validation for every fixture.
- A Go test that loads every fixture definition and rejects duplicate IDs.
- A conformance smoke test that runs at least one fixture end to end.
- CI must fail when a fixture marked `supported` does not pass.

## Dependencies

None. This is M0 and should be implemented first.

## Exit criteria

- At least 12 positive and 8 negative/ambiguous conversations exist.
- Every future planner or frontend change can name the fixtures it enables.
- `make conformance` produces a deterministic result summary.

---

# Gap 2: FHIR Version and Schema Support Is Implicit

## Current state

The server defaults to `schemas/graph-fhir.json`. The generated loader supports
only generated resource cases, while the generic loader requires a matching
class in the graph schema. The loaded project does not persist which FHIR
version, schema digest, generator version, or ingestion mode produced it.

## Target state

Every project load is tied to an explicit, inspectable schema identity. Unknown
profiles and extensions remain in the raw payload. Unsupported resource types
fail during preflight with a complete report rather than partway through load.

## Implementation plan

1. Add `internal/schemaidentity` with:
   - `FHIRVersion`
   - `SchemaName`
   - `SchemaVersion`
   - `SchemaSHA256`
   - `GeneratorVersion`
   - `GeneratedResourceTypes`
2. Compute the schema digest at process startup.
3. Add a `loom_dataset` collection containing one document per project and
   dataset generation.
4. Persist schema identity in the dataset document before ingestion begins.
5. Add ingestion preflight that scans filenames and the first bounded number of
   records to identify resource types.
6. Compare discovered resource types with:
   - graph-schema classes
   - generated loader support
7. Select ingestion mode per resource type:
   - generated when supported
   - generic fallback when the graph schema contains the class
   - unsupported otherwise
8. Remove the requirement that a whole load uses one global `UseGeneric` mode.
   Retain an override for debugging and parity testing.
9. Return one structured preflight report listing every resource type and
   selected mode before writing begins.
10. Add server endpoints/GraphQL fields to inspect active server schema identity
    and dataset schema identity.
11. Reject queries when the dataset schema identity is incompatible with the
    active semantic/schema metadata unless an explicit migration is run.

## Files and packages

- add `internal/schemaidentity/`
- modify `internal/ingest/load.go`
- modify `internal/ingest/row_builder.go`
- modify `internal/ingest/generated_load.go` or generation output contract
- modify `internal/ingest/backend.go`
- modify both command entrypoints

## Tests

- generated-supported resource selects generated mode
- schema-known but nongenerated resource selects generic mode
- schema-unknown resource fails preflight without writes
- extension data survives round-trip in `payload`
- schema digest mismatch is detected
- mixed generated/generic load produces graph-equivalent documents

## Migration

Existing projects have no dataset identity. Mark them `legacy-unversioned` and
require analysis rebuild before exposing them through the product API.

## Exit criteria

- A project always reports the schema under which it was loaded.
- Mixed-resource loads choose the safest available builder automatically.
- Unsupported resource types are reported before mutation.

---

# Gap 3: Loads Are Not Atomic Dataset Operations

## Current state

CLI directory loads and synchronous single-file HTTP imports write directly to
live collections. The HTTP route stages one file and waits for completion. A
failed multi-resource load can leave partially updated data and discovery
metadata.

## Target state

A dataset load is a durable operation with preflight, generation identity,
progress, error accounting, finalization, and an explicit ready state. Queries
only use finalized generations.

## Implementation plan

1. Extend `loom_dataset` with states:
   - `PREFLIGHT`
   - `LOADING`
   - `ANALYZING`
   - `READY`
   - `FAILED`
   - `SUPERSEDED`
2. Assign every load a `dataset_generation` UUID.
3. Store generation on every vertex, edge, and catalog document.
4. Update all indexes and query filters to include project plus active
   generation.
5. Introduce `internal/dataset` to own lifecycle transitions.
6. Split ingestion into:
   - preflight
   - bootstrap
   - resource ingestion
   - reference reconciliation
   - catalog finalization
   - analysis
   - activation
7. Write into a new generation while the previous generation remains readable.
8. Atomically switch the project's active generation only after validation.
9. Retain the prior generation until a configured cleanup period expires.
10. Replace synchronous HTTP import as the primary deployment path with a load
    job endpoint. Keep synchronous import only as a bounded development helper.
11. Persist per-resource counts, rejected rows, validation errors, inserted
    edges, elapsed stages, and source checksums.
12. Add cancellation checks through the load loops.
13. Add a cleanup command for superseded/failed generations.

## Tests

- failed load never replaces active generation
- previous generation remains queryable during a new load
- activation switches all discovery/query reads consistently
- cancellation leaves a failed/cancelled generation, not a ready one
- cleanup removes only inactive generations
- partial resource failure has a durable error report

## Dependencies

Gap 2.

## Exit criteria

- A development operator can observe and retry a failed load.
- No query sees a mixture of old and new generations.
- Dataset readiness is a persisted state, not inferred from collection presence.

---

# Gap 4: Reference Integrity Is Not Measured After Load

## Current state

Edges are generated during resource ingestion. The system counts edges but does
not publish a post-load integrity report covering dangling targets, duplicate
references, cross-project edges, or relationships that resolve only partially.

## Target state

Every finalized dataset has a reference-integrity report used by both operators
and recipe capability analysis.

## Implementation plan

1. Add `internal/analysis/referenceintegrity`.
2. Run bounded AQL analyses for every observed relationship tuple.
3. Record:
   - total references
   - resolved edges
   - dangling `_from` and `_to` counts
   - distinct sources and targets
   - duplicate logical reference count
   - cross-project/generation violations
   - source coverage
4. Persist results in `loom_relationship_analysis` keyed by project,
   generation, source type, label, and target type.
5. Classify relationship quality:
   - `HEALTHY`
   - `SPARSE`
   - `PARTIAL`
   - `BROKEN`
6. Block generation activation only for structural isolation violations.
   Preserve sparse/partial datasets but surface warnings.
7. Feed relationship quality into recipe capability results.
8. Add a CLI inspection command for operators.

## Tests

- complete references produce healthy analysis
- dangling target is counted and warned
- cross-project edge fails activation
- sparse relationship remains usable with a warning

## Dependencies

Gap 3.

## Exit criteria

- Every active dataset has a reference-integrity summary.
- The product never recommends a path without reporting observed coverage.

---

# Gap 5: Catalog Analysis Is Too Shallow for Product Decisions

## Current state

`fhir_field_catalog` records path, kind, document count, sample count, bounded
distinct values, and pivot metadata. Reference discovery reports tuple and edge
count. It does not provide resource counts, coverage denominators, value
frequencies, high-cardinality search, fanout percentiles, or freshness metadata.

## Target state

A finalized, generation-scoped analysis snapshot answers the questions needed
by recipe selection and guided filtering without expensive ad hoc scans on each
page load.

## Implementation plan

1. Create `internal/analysis` as the orchestration package. Keep catalog raw
   reads in `internal/catalog`.
2. Add `loom_resource_analysis`:
   - document count
   - valid/rejected count
   - distinct logical ID count
   - profile/extension URL frequencies where available
3. Extend field analysis with:
   - coverage numerator and denominator
   - missing count
   - scalar/repeated cardinality classification
   - approximate distinct count
   - bounded example values
   - top values with counts for low/medium-cardinality scalar fields
   - sensitivity/display classification hook
4. Extend relationship analysis with:
   - distinct source/target counts
   - source and target coverage
   - average, p50, p95, and max fanout
5. Do not compute unbounded exact distinct values during every load. Introduce
   configurable caps and approximate counts.
6. Add on-demand, paged value search for high-cardinality fields. Require a
   minimum search prefix and enforce query timeouts.
7. Persist `analysis_version`, generation, start/end timestamps, and status.
8. Make analysis idempotent and restartable.
9. Extend cache keys with dataset generation and analysis version.
10. Invalidate cache on generation activation rather than individual resource
    writes.
11. Add service methods for dataset summary, relationship inventory, candidate
    fields, and value suggestions.

## Suggested packages

```text
internal/analysis/
  service.go
  resource.go
  relationships.go
  fields.go
  values.go
  types.go
```

## Tests

- coverage uses the correct resource denominator
- auth scope changes counts and values correctly
- fanout percentiles match a deterministic fixture
- truncated value sets explicitly report truncation
- on-demand value search cannot escape project/generation/auth scope
- analysis rerun replaces one snapshot without duplicate records

## Dependencies

Gaps 3 and 4.

## Exit criteria

- The frontend can render recipe availability, columns, examples, coverage, and
  filters without issuing raw AQL.
- Analysis has a known version and freshness state.

---

# Gap 6: No Versioned Product Recipe Model

## Current state

The public input is a compiler-oriented GraphQL tree of roots, selectors,
traversals, aggregates, pivots, and slices. There is no stable artifact that
captures user intent independently of current planner internals.

## Target state

The product owns a versioned recipe model that can be created by the frontend,
CLI, or future language interface and translated into the existing dataframe
builder.

## Implementation plan

1. Add `internal/recipe`.
2. Define V1 types:
   - `Recipe`
   - `TemplateID`
   - `Grain`
   - `ColumnSelection`
   - `Filter`
   - `Sort` only if required for deterministic slices
   - `Destination`
3. Use stable semantic IDs, not raw AQL or display labels.
4. Include:
   - recipe version
   - template version
   - project
   - dataset generation constraint or compatibility policy
   - row grain
   - selected columns
   - filters
   - user-provided output names
5. Define JSON Schema and GraphQL input/output types.
6. Implement normalization:
   - trim names
   - canonicalize operators
   - deduplicate columns
   - apply explicit defaults
   - produce deterministic serialization
7. Implement `Recipe -> dataframe.Builder` translation behind an interface.
8. Keep `FhirDataframeInput` as an advanced/developer contract during
   migration; do not force the browser product to construct it.
9. Return typed error codes with field paths in addition to messages.
10. Add migration functions from V1 to future recipe versions.

## Tests

- JSON and GraphQL round trips
- deterministic normalization
- duplicate/output-name collision rejection
- V1 fixture translation into expected builder input
- unknown version fails with an actionable error

## Dependencies

Gap 1. It can begin in parallel with analysis but cannot be declared usable
until Gap 7 exists.

## Exit criteria

- User intent can be saved without storing GraphQL or AQL.
- Conformance fixtures use the real recipe types.

---

# Gap 7: No Template and Semantic Vocabulary Registry

## Current state

Friendly field references and some traversal semantics exist, but there is no
product registry defining recipe families, grains, common columns, synonyms,
required relationships, or destination compatibility.

## Target state

Templates are versioned code/data definitions that map user concepts to FHIR
semantics and declare capability requirements without embedding canned AQL.

## Implementation plan

1. Add `internal/recipe/templates`.
2. Define `TemplateDefinition` with:
   - stable ID/version
   - title/description
   - supported grains
   - required and optional semantic relationships
   - suggested/common/advanced columns
   - common filters
   - required planner capabilities
   - stable identifier strategy
   - supported destinations
3. Define `SemanticField` with:
   - semantic ID
   - labels and synonyms
   - candidate resource types/profiles
   - prioritized `fieldRef`/selector fallbacks
   - data kind
   - sensitivity classification
4. Define `SemanticRelationship` separately from physical edge labels.
5. Consolidate the mechanically derived field references in
   `internal/dataframebuilder/fieldrefs.go` and the optimized traversal roles in
   `internal/dataframe/traversal_rules.go` behind these stable concepts. Avoid
   leaving product semantics split across unrelated packages.
6. Implement initial template definitions for all six product families.
7. Add a capability evaluator that intersects:
   - template requirements
   - active dataset analysis
   - planner capabilities
8. Return `AVAILABLE`, `DEGRADED`, or `UNAVAILABLE` with reasons.
9. Add human-readable fallback text, but keep reason codes stable.
10. Make registry validation run in tests and process startup.

## Tests

- every template references valid semantic fields/relationships
- every suggested column has at least one resolver
- capability evaluation handles missing resources, sparse links, and planner
  limitations
- synonyms do not create ambiguous IDs

## Dependencies

Gaps 5 and 6.

## Exit criteria

- The UI can ask "What are you making?" using backend-returned templates.
- An unavailable template includes a concrete reason.

---

# Gap 8: No Unified Capability API

## Current state

The current introspection operation returns fields and one-hop related resource
hints. Clients could combine those records, but they cannot ask whether a full
recipe is supported, what grain is viable, or what warnings apply.

## Target state

One backend service is authoritative for dataset summary, templates, recipe
options, value suggestions, validation, and explanation.

## Implementation plan

1. Add `internal/productapi` or extend `internal/dataframebuilder` only if the
   latter remains semantically accurate.
2. Implement service methods:
   - `DatasetSummary`
   - `ListRecipeTemplates`
   - `RecipeOptions`
   - `FieldValueSuggestions`
   - `ValidateRecipe`
   - `ExplainRecipe`
3. Add matching GraphQL queries while retaining current introspection for
   advanced clients.
4. Every input must resolve project, active generation, and auth paths before
   analysis reads.
5. Return stable support and warning codes.
6. Include analysis freshness and dataset generation in responses.
7. Add request cost limits for recursive option expansion and value search.
8. Cache read-only results by project, generation, auth scope, template, grain,
   and analysis version.

## Tests

- GraphQL contract tests for every new operation
- cross-scope cache isolation
- unavailable template reason propagation
- stale analysis response behavior
- no raw selector/AQL required in the primary recipe-options flow

## Dependencies

Gaps 5-7.

## Exit criteria

- A thin frontend can implement the full conversation using documented API
  calls.
- Backend capability responses never advertise a planner-unsupported path.

---

# Gap 9: No Recipe Persistence and Ownership

## Current state

Recipes cannot be saved, named, versioned, cloned, or audited.

## Target state

Users can save reusable recipes while Loom preserves ownership, normalized
content, compatibility state, and execution history.

## Implementation plan

1. Add `loom_recipe` collection.
2. Store:
   - recipe ID
   - project
   - owner subject
   - name/description
   - normalized recipe JSON
   - recipe/template versions
   - created/updated timestamps
   - last validated generation
   - optimistic concurrency revision
3. Implement create, read, update, clone, list, and archive operations.
4. Enforce project authorization and owner/admin mutation policy.
5. On load, revalidate against the active dataset generation and report:
   - compatible
   - compatible with warnings
   - incompatible
6. Never silently rewrite a saved recipe after template changes.
7. Add explicit migration/upgrade operation producing a new revision.

## Tests

- owner and project authorization
- optimistic concurrency conflict
- generation revalidation
- template upgrade preserves the previous revision
- archived recipes do not appear by default

## Dependencies

Gaps 6-8.

## Exit criteria

- A recipe is a durable, auditable product artifact.
- Loading new data cannot silently change saved intent.

---

# Gap 10: Planner Is Patient-Root and Hardcoded to One Family

## Current state

`internal/dataframe/planner.go` rejects non-Patient roots. Supported traversal
tuples are classified in a hardcoded switch in `traversal_rules.go`. The
lowered plan is optimized around patient/case/assay and document summary sets.

## Target state

The planner supports declared row grains for Patient, Specimen,
DocumentReference/File, Condition/Diagnosis, Observation, and ResearchSubject
or study enrollment. Optimization remains explicit, testable, and safe.

## Implementation plan

This gap is superseded by CP0-CP9 in
[`COMPILER_FIRST_PLAN.md`](COMPILER_FIRST_PLAN.md). The steps below remain a
summary only; do not assign Gap 10 as one worker packet.

1. Refactor planning into three layers:
   - semantic logical plan
   - generic physical plan
   - optional optimized physical rewrites
2. Introduce `LogicalPlan` types that explicitly represent:
   - grain/root set
   - joins/traversals
   - projections
   - filters
   - grouping/aggregation
   - pivots
   - representative slices
3. Replace the Patient check with grain-specific root planning.
4. Implement a generic one-hop/n-hop traversal physical operator using
   schema-validated relationship definitions.
5. Preserve current patient-case-assay logic as an optimization rule over the
   generic plan, not the only compilable shape.
6. Replace the switch-only traversal registry with data-driven semantic rules
   generated or validated against `fhirschema`.
7. Add planner capability descriptors so Gap 7 can ask what operations are
   implemented for each grain/path.
8. Implement grains in this order:
   - Patient
   - Specimen
   - DocumentReference/File
   - Condition/Diagnosis
   - Observation
   - ResearchSubject/Study enrollment
9. For each grain, define stable row identity and duplicate semantics.
10. Reject cycles and cap traversal depth in V1.
11. Add plan normalization to ensure semantically equivalent recipes compile
    consistently.
12. Add plan explain output separate from raw AQL.

## Suggested file split

```text
internal/dataframe/planner/
  logical.go
  capabilities.go
  validate.go
  physical.go
  generic.go
  optimize_patient.go
  explain.go
```

Perform the move incrementally; do not rewrite the compiler and planner in one
unreviewable change.

## Tests

- golden logical plans for every conformance recipe
- root-grain tests for all six grains
- no duplicate rows unless the recipe explicitly requests an exploding grain
- cycle/depth rejection
- old Patient queries compile to equivalent AQL/results
- generic and optimized plans have result parity

## Dependencies

Gaps 1, 6, and 7.

## Exit criteria

- Every initial template has at least one supported grain.
- Planner support is queryable rather than inferred from error strings.
- Current optimized Patient behavior remains covered by parity tests.

---

# Gap 11: Filters and FHIR Selection Semantics Are Too Narrow

## Current state

Selectors support a constrained path grammar and predicates are largely
`contains`/equality-oriented. The recipe experience needs typed filters,
missing-value rules, terminology-aware code selection, numeric/date comparison,
and repeated-value semantics.

## Target state

Recipe filters use a typed, safe expression model compiled to bound AQL. The
supported subset is explicit and sufficient for the six initial templates.

## Implementation plan

1. Define typed recipe filter operators:
   - `EQUALS`
   - `NOT_EQUALS`
   - `IN`
   - `EXISTS`
   - `MISSING`
   - `CONTAINS_TEXT`
   - `GT`, `GTE`, `LT`, `LTE`
   - bounded date/range operators
2. Define repeated-value semantics:
   - `ANY`
   - `ALL` only if required
   - `NONE`
3. Add data types to semantic fields and candidate-column responses.
4. Validate operator compatibility before planning.
5. Compile all values through bind variables; never interpolate user values.
6. Add terminology representation for code/system/display triples. Do not rely
   only on display strings when a code is available.
7. Implement missing/null semantics consistently for absent path, null value,
   and empty array.
8. Add timezone policy for FHIR date/dateTime/instant comparisons.
9. Expose filter capabilities per field through Recipe Options.
10. Add cost limits for large `IN` lists.

## Tests

- operator/type compatibility
- array `ANY` semantics
- missing versus empty versus null
- code/system matching
- date boundary behavior
- bind-variable injection safety
- auth filters remain present in every compiled traversal

## Dependencies

Gaps 6, 7, and 10.

## Exit criteria

- Every initial conversation fixture can express its filters without raw
  FHIRPath or AQL.
- Filter semantics have result-based tests, not only query-string tests.

---

# Gap 12: Cardinality and Query Cost Are Not Explained

## Current state

The compiler can aggregate and slice, but the product does not explain whether
a relationship multiplies rows, produces arrays, or creates a costly plan.
Users can request a technically valid shape that is surprising or expensive.

## Target state

Recipe validation returns row-grain, cardinality, null-coverage, and cost
warnings before preview or export.

## Implementation plan

1. Add `PlanAnalysis` to the planner result.
2. For every relationship classify:
   - one-to-one observed
   - optional-one observed
   - one-to-many
   - many-to-many/unknown
3. Use dataset fanout analysis from Gap 5.
4. Estimate base rows, output rows, scanned vertices, and maximum fanout.
5. Mark each selected column as:
   - scalar
   - repeated array
   - aggregated
   - pivoted
   - row-expanding
6. Return stable warnings such as:
   - `HIGH_FANOUT`
   - `SPARSE_COLUMN`
   - `ROW_EXPANSION`
   - `HIGH_CARDINALITY_PIVOT`
   - `EXPENSIVE_VALUE_FILTER`
7. Add configurable preview/export cost policies.
8. Require explicit acknowledgement or asynchronous export for plans above the
   synchronous threshold.
9. Surface AQL optimizer/explain diagnostics in developer mode only.

## Tests

- deterministic cardinality estimates on fixture datasets
- warning thresholds
- high-fanout plan cannot run synchronously
- scalar versus array output classification
- developer explain contains no credentials or sensitive values

## Dependencies

Gaps 5 and 10.

## Exit criteria

- The user can understand what one row means before running the query.
- Loom blocks obviously unsafe synchronous plans.

---

# Gap 13: Preview Is Not a Bounded Production API

## Current state

Preview uses a row limit but collects every returned row in a Go slice. Cursor
input is not a real paging implementation. There are no explicit query timeout,
concurrency, cancellation, response-size, or stable-order guarantees.

## Target state

Preview is a bounded, cancellable API with deterministic pagination and resource
limits.

## Implementation plan

1. Create a preview-specific service rather than overloading export behavior.
2. Define hard server limits:
   - maximum requested rows
   - maximum encoded bytes
   - maximum query duration
   - maximum concurrent previews per subject/project
3. Establish a stable ordering and cursor contract based on grain identity plus
   a tie-breaker.
4. Compile cursor predicates rather than offset pagination.
5. Return `nextCursor`, truncated state, elapsed time, and warnings.
6. Cancel the Arango cursor when the request context ends.
7. Stop row iteration when encoded-byte or row limits are reached.
8. Reject preview for plans above the configured synchronous cost threshold.
9. Keep a small in-memory result only up to the enforced maximum.
10. Add request-level metrics and structured error codes.

## Tests

- stable next-page behavior with duplicate sort values
- cancellation closes the query cursor
- byte limit truncates safely
- maximum concurrency enforcement
- timeout maps to a stable API error
- authorization scope is identical across pages

## Dependencies

Gaps 10-12.

## Exit criteria

- Preview cannot consume unbounded Loom memory.
- Pagination neither skips nor duplicates rows for a stable dataset generation.

---

# Gap 14: No Shared Streaming Export Runtime

## Current state

The row executor uses a callback, but the dataframe service materializes all
rows into `Result`. File export remains CLI/canned-query territory.

## Target state

One streaming runtime executes a compiled plan and feeds bounded destination
writers without materializing the full dataframe.

## Implementation plan

1. Define `RowStream`/`RowSink` interfaces in `internal/export` or
   `internal/dataframe/stream`.
2. Make the stream provide:
   - schema/columns before or with first row
   - rows
   - progress counters
   - close/cancel/error semantics
3. Refactor query execution so preview and export share compilation but use
   different consumers.
4. Implement NDJSON writer with one flat object per line.
5. Implement CSV writer with deterministic columns and configurable array
   encoding policy.
6. Add checksum, byte count, row count, start/end timestamps, and recipe/plan
   provenance.
7. Write to a temporary artifact and atomically finalize it.
8. Define an artifact-store interface. Implement local filesystem first, with
   configuration that makes later object storage possible.
9. Add maximum artifact size and disk-space preflight.
10. Ensure cancellation deletes incomplete temporary artifacts or marks them
    failed for cleanup.

## Tests

- million-row synthetic stream keeps memory bounded
- NDJSON is valid and newline-terminated
- CSV columns remain stable when rows omit values
- cancellation removes incomplete artifact
- checksum and counts match content
- preview/export result parity for the same recipe and generation

## Dependencies

Gaps 10-13.

## Exit criteria

- Export memory usage is independent of total row count.
- CSV and NDJSON use the same compiled plan and row semantics.

---

# Gap 15: No Durable Job System

## Current state

Imports run synchronously and exports do not exist. There is no persistent
queue, lease, retry, cancellation, progress, or recovery after process restart.

## Target state

Load, analysis, export, and Elasticsearch delivery execute as durable jobs with
observable state.

## Implementation plan

Implement this gap in three packets:

- **15A:** generic job substrate, required by Gap 3 dataset load jobs
- **15B:** export recovery and row-stream progress, after Gap 14
- **15C:** retention and operational hardening

1. Add `loom_job` collection and `internal/jobs` as 15A.
2. Define job types:
   - `DATASET_LOAD`
   - `DATASET_ANALYSIS`
   - `DATAFRAME_EXPORT`
   - `ELASTICSEARCH_LOAD`
   - `GENERATION_CLEANUP`
3. Define states:
   - `QUEUED`
   - `RUNNING`
   - `SUCCEEDED`
   - `FAILED`
   - `CANCELLING`
   - `CANCELLED`
4. Persist payload reference, project, generation, owner, progress, attempts,
   lease owner/expiry, timestamps, result, and structured error.
5. Implement an in-process worker using Arango-backed leasing first. Keep the
   queue interface replaceable.
6. Use atomic compare/update for job claims.
7. Renew leases and reclaim expired jobs after restart.
8. Classify retryable versus terminal errors.
9. Make destination writes idempotent or resume-safe before enabling retries.
10. Add create/status/list/cancel API operations.
11. Enforce per-project and global concurrency.
12. Add retention and cleanup policies.

## Tests

- two workers cannot own one lease
- expired lease is reclaimed
- process restart resumes or safely retries work
- cancellation propagates to Arango query and sink
- terminal validation errors are not retried
- status authorization prevents cross-project access

## Dependencies

Gap 2 for identity fields. Gap 3 depends on 15A. Gap 14 is required for 15B
export behavior. Gap 18 consumes 15C operational behavior.

## Exit criteria

- A server restart does not erase job history or strand running work forever.
- Operators and users can see progress and failure reasons.

---

# Gap 16: No Elasticsearch Destination

## Current state

Loom does not validate an Elasticsearch target, create bulk payloads, handle
partial bulk failures, or record delivery provenance.

## Target state

A validated recipe can stream flat documents into Elasticsearch through a
durable job with deterministic IDs, bounded batches, retry safety, and an
auditable result.

## Implementation plan

1. Add `internal/export/elasticsearch` implementing the shared row sink.
2. Define destination configuration by secret reference, never inline password:
   - endpoint
   - credential/secret reference
   - index or alias
   - TLS settings
   - operation mode (`create` or `index`)
3. Add connection and permission preflight.
4. Require a deterministic document ID strategy. Default to a stable hash of:
   - project
   - dataset generation or logical dataset identity
   - recipe ID/version
   - row-grain identity
5. Validate that every recipe can produce the identity fields before enabling
   Elasticsearch.
6. Implement NDJSON Bulk API encoding with action and source lines.
7. Batch by both document count and encoded bytes.
8. Parse every bulk item response. Treat HTTP success with item failures as a
   partial failure.
9. Retry only retryable items with capped exponential backoff and jitter.
10. Persist success/failure counts and a bounded error sample.
11. Provide optional mapping preflight:
    - infer Loom output types
    - compare against existing mappings
    - report conflicts before job start
12. Do not silently create or replace production index templates. Make index
    provisioning an explicit policy/configuration.
13. Support alias-based promotion as a later, separate operation if full index
    rebuild workflows require it.
14. Redact endpoint credentials and sensitive row values from logs.

## Tests

- exact bulk wire format and final newline
- byte/count batch boundaries
- deterministic IDs across retries
- 429/503 retry behavior
- mixed-success bulk response handling
- mapping conflict preflight
- cancellation and timeout
- secrets absent from logs and persisted job payloads

## Dependencies

Gaps 14 and 15.

## Exit criteria

- A retry cannot create uncontrolled duplicate documents.
- Partial failures are visible and attributable.
- Elasticsearch delivery and file export have identical row content.

---

# Gap 17: Authorization Is Not Yet Proven End to End

## Current state

Project and auth-resource-path scoping exists across discovery and dataframe
queries. Production features will add analysis snapshots, saved recipes, jobs,
artifacts, and external destinations, all of which create new authorization
surfaces.

## Target state

The same resolved principal scope applies to discovery, validation, preview,
export, artifact access, and Elasticsearch delivery. No cached or persisted
derived data leaks information across scopes.

## Implementation plan

1. Write an authorization matrix covering every API operation and stored
   collection.
2. Centralize project/scope resolution in a request-scoped service dependency.
3. Store the effective auth scope or an immutable scope fingerprint on jobs and
   artifacts.
4. Decide whether saved recipes store requested scope, inherit runtime scope, or
   both. Prefer runtime reauthorization.
5. Ensure analysis responses are either:
   - computed per auth scope; or
   - safely filtered from scope-partitioned aggregates
6. Include generation and auth scope in all cache keys.
7. Authorize artifact download independently from job creation.
8. Add SSRF protections for Elasticsearch endpoints:
   - configured allowlist or administrator-managed destinations
   - block arbitrary user-provided internal URLs
9. Add audit events for load, recipe mutation, export creation, artifact access,
   and external delivery.
10. Add query and export rate limits by subject/project.

## Tests

- two auth paths in one project see different analysis and preview results
- cache cannot leak values between scopes
- job creator losing access cannot download artifact
- arbitrary Elasticsearch URL is rejected
- audit event contains identity and action but not sensitive data

## Dependencies

Cross-cutting. Add tests as each earlier gap lands; complete before M5.

## Exit criteria

- An end-to-end isolation suite covers discovery through destination delivery.
- Security-sensitive destinations are administrator-controlled.

---

# Gap 18: Service Operations and Deployment Are Incomplete

## Current state

The server has a liveness endpoint and local flags. There is no readiness check,
graceful worker drain contract, formal configuration validation, deployment
manifest, or dependency health report.

## Target state

Loom can be deployed to a development server, restarted safely, configured by
environment/secret references, and inspected by an operator.

## Implementation plan

1. Separate liveness and readiness:
   - `/healthz`: process alive
   - `/readyz`: Arango reachable, schema compatible, migrations complete,
     artifact store writable, worker ready
2. Add startup configuration validation and a sanitized configuration summary.
3. Add graceful shutdown:
   - stop accepting new jobs
   - cancel/finish bounded HTTP requests
   - release or expire worker leases
   - close Arango clients
4. Add explicit database migration/version collection.
5. Add container image and non-root runtime verification.
6. Add a development deployment example, preferably Helm values/manifests if
   that matches the surrounding platform.
7. Move secrets to environment/file references.
8. Configure request body, preview, query, job, artifact, and retention limits.
9. Add backup/restore notes for Loom metadata collections separately from raw
   reloadable FHIR data.
10. Add an operator runbook for:
    - failed load
    - stale analysis
    - stuck job
    - disk pressure
    - Arango outage
    - Elasticsearch partial failure

## Tests

- readiness fails when Arango is unavailable
- readiness fails for pending migration
- SIGTERM releases work safely
- invalid configuration fails before serving traffic
- container smoke test loads a small dataset and previews a recipe

## Dependencies

Gaps 2, 3, 14, and 15.

## Exit criteria

- A clean development environment can deploy Loom from documented artifacts.
- Restart and dependency failure behavior is documented and tested.

---

# Gap 19: Observability and Performance Budgets Are Missing

## Current state

The repository emits logs and ingest progress events, but it does not define
service-level metrics or budgets for analysis, planning, preview, export, and
jobs.

## Target state

Operators can determine whether Loom is healthy and developers can detect
performance regressions before deployment.

## Implementation plan

1. Add Prometheus-compatible metrics for:
   - request count/latency/error by operation
   - active Arango queries
   - planner duration and support failures
   - preview rows/bytes/duration
   - job queue depth and age
   - job duration/retries/failures
   - export rows/bytes/throughput
   - Elasticsearch item retries/failures
   - analysis age and duration
   - cache hit/miss/eviction
2. Add structured fields:
   - request ID
   - job ID
   - project
   - generation
   - recipe ID
   - plan profile
   Do not log PHI field values.
3. Define initial budgets:
   - recipe options p95
   - validation/explain p95
   - preview p95 for fixture sizes
   - maximum preview memory
   - export throughput and memory
4. Add benchmark fixtures at small, medium, and development-scale sizes.
5. Capture Arango `EXPLAIN` output for golden performance cases and assert
   critical indexes/plan properties without overfitting the full plan text.
6. Add load tests for concurrent previews and background exports.
7. Document measurement commands and expected ranges.

## Tests

- metrics endpoint smoke test
- sensitive value redaction
- benchmark memory assertions for streaming export
- performance regression thresholds in a nonflaky scheduled CI job

## Dependencies

Instrument each gap as implemented; finish after Gap 16.

## Exit criteria

- A slow preview or stalled export can be diagnosed without attaching a
  debugger.
- Product-critical paths have written budgets and repeatable benchmarks.

---

# Gap 20: Test Coverage Does Not Yet Prove Product Generality

## Current state

There are strong focused unit tests and some Arango integration tests, but no
matrix proving mixed-resource ingestion, analysis, multiple row grains,
authorization isolation, restart recovery, and delivery parity.

## Target state

CI and scheduled tests prove the supported product envelope.

## Implementation plan

1. Organize tests into:
   - fast unit tests
   - contract tests
   - conformance tests
   - Arango integration tests
   - end-to-end deployment smoke tests
   - scheduled performance tests
2. Build dataset fixtures representing:
   - complete research dataset
   - sparse dataset
   - dangling references
   - extension-heavy resources
   - multiple auth paths
   - high-fanout observations
   - high-cardinality filter values
3. For every supported recipe, assert:
   - availability
   - normalized recipe
   - logical plan
   - output schema
   - row identity/cardinality
   - preview/export parity
4. Add generated/generic ingestion parity for every generated resource type.
5. Add fuzz tests for selector parsing, recipe normalization, cursor decoding,
   and bulk response parsing.
6. Add failure injection for Arango cursor errors, filesystem full, worker
   restart, and Elasticsearch partial failure.
7. Add compatibility tests for saved V1 recipes across future template/planner
   changes.
8. Publish the supported capability matrix from conformance results.

## Dependencies

Gap 1 and all implementation gaps as they land.

## Exit criteria

- "Supported" means a passing end-to-end fixture, not an available UI control.
- The development deployment runs the same conformance suite used by CI.

---

# 5. Thin Frontend Plan

The frontend is intentionally not a numbered backend gap because it should be
built after M2 exposes honest capabilities.

Implement it in this order:

1. project/dataset readiness screen
2. backend-provided recipe gallery
3. "one row per" grain selection
4. suggested/searchable populated columns
5. guided filters using value suggestions
6. validation and cardinality warnings
7. bounded preview
8. save recipe
9. start/export job and status view
10. advanced developer diagnostics behind an explicit disclosure

Frontend acceptance test:

> A user who knows the desired data but does not know FHIRPath, GraphQL, AQL,
> or Loom can complete the supported conformance tasks without developer help.

The former hard-coded `/builder` demo was removed: it encoded one GDC-shaped
graph editor and could not use the compiler-safe capability contract. Developer
diagnostics should use compiler explanations and conformance fixtures; the
product interface should be the guided recipe flow.

# 6. Recommended Execution Order

An implementation agent should use this dependency-aware sequence:

1. CP0: compiler corpus, reference semantics, and baselines
2. CP1-CP2: semantic IR and generated FHIR graph semantics
3. CP3-CP4: row grain/cardinality and typed filters
4. CP5-CP6: correct generic lowering plus aggregate/pivot engine
5. CP7: typed AQL physical IR and code generation
6. CP8: explicit optimization passes
7. CP9: Arango EXPLAIN, cost, indexes, and performance gates
8. Reduced G6-G8: recipe presets and thin capability API over the compiler
9. Reduced G12-G14: explain, bounded preview, and row-stream sinks
10. G2-G5 as needed for durable arbitrary dataset lifecycle and statistics
11. G15-G20 for jobs, destinations, deployment, security, and release proof

Recipe persistence and frontend expansion should follow compiler validation and
explain. They are not prerequisites for compiler implementation.

For every gap, the implementing agent must:

1. inspect the named current owners before editing
2. add or update public types before generated GraphQL artifacts
3. keep authorization and generation in every query
4. write unit tests before or with implementation
5. add integration evidence for database behavior
6. update the capability matrix
7. run `go test ./...` plus the relevant conformance target
8. update developer documentation when the runtime contract changes

# 7. First Development-Server Release Gate

The first deployable development release does not require every conceivable
FHIR query. It does require all of the following:

- explicit FHIR/schema identity
- automatic generated/generic ingestion selection
- atomic dataset generation activation
- load and analysis jobs with persisted status
- dataset/resource/relationship/field analysis
- versioned recipes and six registered template families
- capability API that hides unsupported templates and paths
- at least one passing supported recipe for every advertised family
- Patient, Specimen, File, Diagnosis, Observation, and Study Enrollment grains
- typed filters required by conformance fixtures
- cardinality and cost warnings
- bounded, cancellable preview
- streaming NDJSON and CSV export
- saved recipes
- end-to-end authorization isolation tests
- readiness, graceful shutdown, metrics, and runbook
- a published capability matrix produced by tests

Elasticsearch delivery should be part of the same release only if deterministic
row IDs, partial-failure handling, and retry safety are complete. Otherwise ship
file export first and keep Elasticsearch disabled rather than exposing an
unreliable destination.

# 8. Definition of Done for the Product Direction

Loom has moved beyond demoware when this scenario is routine:

1. An operator uploads or mounts an unfamiliar, schema-compatible FHIR NDJSON
   dataset.
2. Loom preflights every resource type and reports its ingestion strategy.
3. The load completes into a new immutable dataset generation.
4. Loom measures resources, fields, values, references, integrity, coverage,
   and fanout.
5. The API offers only recipe templates supported by both the data and planner.
6. A non-technical user selects what they are making, one row per entity,
   desired columns, and guided filters.
7. Loom returns a normalized recipe, warnings, and an understandable row-grain
   explanation.
8. Preview is fast, bounded, and representative.
9. Export survives process restart, streams with bounded memory, and records
   provenance.
10. The resulting flat data is identical whether written as NDJSON, CSV, or
    delivered to Elasticsearch.
11. Operators can observe, cancel, retry, and diagnose every long-running step.
12. The advertised capability is backed by passing conformance tests.

That is the production boundary this plan is designed to reach.
