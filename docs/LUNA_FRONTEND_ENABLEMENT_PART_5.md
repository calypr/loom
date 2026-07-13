# Part 5: Luna frontend-enablement execution plan

## Mission

Build the thin product API needed for a nontechnical frontend without moving
FHIR knowledge, graph safety, AQL generation, authorization, or dataset
generation logic into the browser.

This part starts from a working compiler and execution service. It does not
redesign the physical compiler. It adds six frontend-facing capabilities:

1. dataset and root-resource discovery;
2. guided dataframe templates;
3. compile/validate without execution;
4. structured frontend errors;
5. stable preview paging;
6. streaming NDJSON and CSV export.

The resulting primary flow is:

```text
list datasets
  -> choose a guided template
  -> inspect available fields, filters, traversals, and pivots
  -> validate and normalize the request
  -> preview bounded pages
  -> stream NDJSON or CSV
```

Saved recipes, durable jobs, Elasticsearch delivery, a large frontend, and
additional AQL optimization tournaments are outside Part 5.

## Repository facts Luna must preserve

Read these files before changing code:

- `README.md`
- `docs/QUICKSTART.md`
- `docs/DEVELOPER_ARCHITECTURE.md`
- `docs/FORMAL_GAP_ANALYSIS.md`
- `graphqlapi/schema.graphqls`
- `graphqlapi/handler.go`
- `graphqlapi/schema.resolvers.go`
- `graphqlapi/output_mapping.go`
- `graphqlapi/dataframe/service.go`
- `graphqlapi/dataframe/introspection.go`
- `graphqlapi/dataframe/input_resolution.go`
- `graphqlapi/dataframe/input_mapping.go`
- `internal/dataframe/service.go`
- `internal/dataframe/execution.go`
- `internal/dataframe/compile.go`
- `internal/dataframe/builder_types.go`
- `internal/catalog/types.go`
- `internal/catalog/read_fields.go`
- `internal/catalog/read_references.go`
- `internal/dataset/active_resolver.go`
- `internal/dataset/manifest.go`
- `internal/httpapi/server.go`
- `internal/httpapi/routes.go`
- `cmd/arango-fhir-server/main.go`
- `fhirschema/`
- `examples/meta_gdc_case_matrix.graphql`
- `examples/meta_gdc_case_matrix.variables.json`

Current, real foundations:

- `dataframeBuilderIntrospection` exposes populated fields, references,
  distinct values, pivot candidates, and relationship cardinality.
- `runFhirDataframe` resolves `fieldRef` values, validates the request, lowers
  through the physical compiler, executes AQL, and returns flattened rows.
- `dataframe.Service.Stream` already delivers flattened rows without retaining
  the entire result in Loom memory.
- active immutable dataset generation resolution is available through
  `dataset.ActiveManifestResolver`.
- request principal and authorization-path resolution already exist in
  `internal/authscope`.
- the compiler owns FHIR selectors and routes through `fhirschema` and
  `resolveStorageRoute`.

Do not duplicate any of these foundations in a new product package.

## Global contracts

Every work package must preserve the following:

1. The browser never constructs AQL and never decides whether a FHIR route is
   physically safe.
2. Public field choices use stable `fieldRef` values. Raw selectors may be
   returned for diagnostics but are not required by the primary UI.
3. Project, active generation, and resolved authorization scope are selected
   once per request and propagated into every catalog, validation, preview,
   and export operation.
4. A restricted caller with no surviving auth-resource paths receives no data;
   an empty list must never be reinterpreted as unrestricted.
5. Only READY active generations are advertised in generation-aware mode.
6. Legacy mutable META mode remains supported deliberately. Its generation is
   represented consistently as absent/null, not invented.
7. Requests and cursor tokens never embed raw AQL, credentials, or unvalidated
   collection names.
8. Output row semantics, column names, pivot flattening, stable root ordering,
   and null/array behavior remain identical between preview and export.
9. All lists returned to the frontend have deterministic ordering.
10. No Patient-, Observation-, GDC-, fixture-, or edge-label-specific branch is
    allowed in compiler, catalog, pagination, or export code.
11. Template definitions may name FHIR resource types and semantic field
    preferences because they are product metadata, but availability must be
    proven from the current catalog and `fhirschema`.
12. GraphQL generated files are generated, never manually edited.
13. Preserve unrelated dirty-worktree changes. Stop if an owned file changes
    concurrently.

## Part 5 public API target

The coordinator should freeze this semantic API before workers begin. Exact
GraphQL naming can change during schema review, but the information and
behavior must remain stable.

```graphql
type Query {
  dataframeDatasets: [DataframeDataset!]!

  dataframeTemplates(
    input: DataframeTemplateOptionsInput!
  ): [DataframeTemplateAvailability!]!

  dataframeBuilderIntrospection(
    input: DataframeBuilderIntrospectionInput!
  ): DataframeBuilderIntrospection!

  validateFhirDataframe(
    input: FhirDataframeInput!
  ): FhirDataframeValidation!
}

type Mutation {
  previewFhirDataframe(
    input: FhirDataframeInput!
    first: Int = 25
    after: String
  ): FhirDataframePage!
}
```

`runFhirDataframe` may remain temporarily as the compatibility name during
Part 5, but it must call the same preview service. Do not maintain two
compilation or execution implementations.

Streaming export is an authenticated HTTP endpoint because GraphQL JSON should
not buffer large row sets:

```text
POST /api/v1/dataframes/export
Content-Type: application/json
Accept: application/x-ndjson | text/csv
```

The request body contains one `FhirDataframeInput`-equivalent request plus an
explicit format. The HTTP adapter must call the same preparation and streaming
service as GraphQL.

## Shared-file ownership and coordination

Only the Part 5 coordinator may edit or regenerate:

- `graphqlapi/schema.graphqls`
- `graphqlapi/generated.go`
- `graphqlapi/model/models.go`
- `graphqlapi/schema.resolvers.go`
- `graphqlapi/resolver.go`
- `graphqlapi/handler.go`
- `graphqlapi/output_mapping.go`
- `cmd/arango-fhir-server/main.go`
- `internal/httpapi/server.go`
- `internal/httpapi/routes.go`
- `README.md`
- `docs/QUICKSTART.md`

Workers implement typed Go services behind coordinator-frozen interfaces and
submit schema wiring requirements as handoff notes. Do not allow multiple
workers to run `make generate-graphql` concurrently.

The coordinator performs GraphQL generation once after WP1-WP5 service
contracts are integrated, then again only if the schema changes during final
review.

## Baseline required before implementation

Run and record:

```bash
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./graphqlapi ./graphqlapi/dataframe ./internal/dataframe ./internal/catalog ./internal/dataset/... ./internal/httpapi -count=1
GOCACHE="$(pwd)/.gocache" GOTOOLCHAIN=auto go test ./conformance/compiler -count=1
make dataframe-demo DATAFRAME_LIMIT=25 DATAFRAME_REPEAT=1 DATAFRAME_PRINT_RESPONSE=false
```

Record the loaded database, project, generation mode, result hash, row count,
and current GraphQL schema hash. A failing baseline is a coordinator decision;
workers must not silently fix unrelated failures.

---

## WP1 — Dataset and root-resource discovery

### Goal

Let a frontend discover what can be queried before it knows a project or root
resource type. Return only datasets and resource types visible to the current
principal.

### Owner and files

**Owner:** dataset-discovery worker.

Owned production files:

- new `internal/catalog/read_datasets.go`
- new `internal/catalog/read_datasets_test.go`
- additive types in `internal/catalog/types.go`
- new `graphqlapi/dataframe/datasets.go`
- new `graphqlapi/dataframe/datasets_test.go`
- additive service dependencies in `graphqlapi/dataframe/service.go`
- additive response types in `graphqlapi/dataframe/types.go`

Coordinator-owned integration:

- GraphQL schema, generated output, resolver, output mapping, server wiring.

### Contract

Define persistence-neutral read types:

```go
type DatasetSummaryOptions struct {
    arangostore.ConnectionOptions
    ProjectAllowlist []string
    CursorBatch int
}

type DatasetSummary struct {
    Project string
    DatasetGeneration string
    State string
    ResourceTypes []ResourceTypeSummary
}

type ResourceTypeSummary struct {
    ResourceType string
    DocumentCount int64
    PopulatedFieldCount int
    PivotCandidateCount int
}
```

Names may be adjusted to match repository conventions, but do not expose
Arango documents or collection names publicly.

### Implementation

1. Determine the caller's allowed projects from the existing principal and
   authorizer contract. Never discover all projects and filter them only in the
   browser.
2. In generation-aware mode, resolve each allowed project's active READY
   manifest. Exclude projects without a valid active READY generation and
   report them only through server logs/diagnostics.
3. In legacy mode, aggregate dataset summaries from catalog rows whose
   generation is null/absent.
4. Obtain resource types from `fhir_field_catalog`, not Arango collection names.
   This avoids advertising system, edge, lifecycle, or generated collections.
5. Aggregate per resource type:
   - maximum or otherwise semantically correct document count from catalog
     facts;
   - populated field count;
   - pivot-candidate count.
6. Apply generation and authorization scope before aggregation. A restricted
   user must not infer resource types that exist only outside their scope.
7. Sort datasets by project and resource types by resource type.
8. Add a cache keyed by principal scope, project, active generation, and
   analysis/catalog version only if profiling proves the query needs it. Do not
   key only by project.
9. Provide a GraphQL service method that returns DTOs without exposing catalog
   persistence types.
10. Keep project selection explicit for deployments whose authorization layer
    cannot enumerate projects. In that case accept a configured project list;
    do not fall back to unrestricted catalog scanning.

### Tests

- legacy null-generation dataset;
- READY active generation;
- project with no active generation;
- active pointer naming a non-READY generation;
- two resource types with deterministic ordering;
- restricted scope hiding one resource type entirely;
- restricted-empty scope returns no resources;
- no leakage between projects or generations;
- catalog contains duplicate auth-path rows but counts are not multiplied;
- no field catalog rows returns an empty list, not null.

### Acceptance

- A caller can reach the first frontend screen without hardcoded root types.
- Every advertised resource type has at least one visible populated catalog
  fact.
- No raw collection discovery or hardcoded FHIR resource list is used.
- Focused unit, GraphQL integration, and live META tests pass.
- Live query completes below 250 ms warm for the current META fixture.

### Handoff

Report exact DTOs, authorization behavior, AQL hash, selected indexes, live
latency, and GraphQL fields required from the coordinator.

---

## WP2 — Guided dataframe template registry

### Goal

Implement the backend answer to “What are you making?” without building a
general saved-recipe system.

Initial template families:

- patient cohort;
- specimen inventory;
- file manifest;
- diagnoses;
- labs/observations;
- study enrollment.

### Owner and files

**Owner:** template worker.

Owned files:

- new `internal/dataframe/template/types.go`
- new `internal/dataframe/template/registry.go`
- new `internal/dataframe/template/availability.go`
- new `internal/dataframe/template/registry_test.go`
- new `internal/dataframe/template/availability_test.go`
- new `graphqlapi/dataframe/templates.go`
- new `graphqlapi/dataframe/templates_test.go`

Read-only dependencies:

- `fhirschema/`
- `internal/catalog`
- existing dataframe input/semantic types.

Coordinator owns GraphQL schema and mappings.

### Template definition

Each template is immutable product metadata:

```go
type Definition struct {
    ID string
    Version int
    Label string
    Description string
    RootCandidates []string
    SuggestedColumns []ColumnSuggestion
    SuggestedTraversals []TraversalSuggestion
    SuggestedPivots []PivotSuggestion
}
```

Suggestions must use semantic preferences rather than raw AQL. A column may
contain ordered `fieldRef` alternatives so the registry can adapt to different
FHIR encodings:

```go
type ColumnSuggestion struct {
    ID string
    Label string
    FieldRefAlternatives []string
    DefaultSelected bool
    Advanced bool
}
```

Traversal suggestions name FHIR source/target types and a semantic route
preference. Availability resolution must match them against discovered
references and `fhirschema`; it must not invent edge labels.

### Implementation

1. Freeze stable IDs and version `1` for all six families.
2. Keep definitions deterministic and side-effect free. No database calls from
   `registry.go`.
3. Resolve availability against the WP1 dataset summary and existing builder
   introspection:
   - viable root types;
   - available suggested columns;
   - unavailable optional suggestions;
   - required missing capabilities;
   - viable traversals and pivots.
4. Return one of `AVAILABLE`, `PARTIAL`, or `UNAVAILABLE` with machine-readable
   reasons.
5. Return `commonColumns` before `advancedColumns` and preserve registry order.
6. Never claim a field is available because it exists in the FHIR schema alone;
   it must be populated and visible in the current dataset catalog.
7. Produce a starter `FhirDataframeInput`-equivalent DTO containing only
   suggestions proven available. Do not persist it.
8. Use the existing `fieldRef` resolution path when materializing the starter
   request; do not copy selector parsing.
9. Keep synonyms and friendly labels in the template package. The compiler
   remains unaware of “file manifest” or “patient cohort.”
10. Document how a seventh template can be added without editing GraphQL or the
    compiler.

### Minimum semantic intent

The exact available fields vary by dataset, but definitions should express:

- patient cohort: Patient grain, identifiers/demographics, optional Condition
  and enrollment relationships;
- specimen inventory: Specimen grain, identifiers, specimen type, subject and
  container/collection facts when populated;
- file manifest: DocumentReference grain or a viable root leading to it,
  attachment name/URL/size/content type and related subject/specimen IDs;
- diagnoses: Condition grain or Patient cohort with Condition aggregation;
- labs/observations: Observation grain or subject grain with bounded
  Observation pivots;
- study enrollment: ResearchSubject grain with proven ResearchStudy and
  subject relationships.

These are product preferences, not assumptions that every dataset contains the
same paths.

### Tests

- all six IDs unique and stable;
- deterministic registry order;
- complete, partial, and unavailable datasets;
- alternative fieldRef selection;
- suggested traversal absent from catalog;
- schema-known but unpopulated field remains unavailable;
- pivot template with zero, one, and many bounded columns;
- restricted scope changes availability without leaking hidden facts;
- generated starter input passes the real WP3 validator;
- no template source imports Arango or renderer packages.

### Acceptance

- The frontend can render six choices without understanding FHIR routes.
- Every enabled suggestion is accepted by real input preparation.
- Unavailable templates explain which capability is missing.
- Adding a template requires registry data and tests, not compiler branches.

### Handoff

Report template IDs/version, availability DTO, starter request DTO, missing
capability reason codes, and coordinator GraphQL requirements.

---

## WP3 — Compile and validate without execution

### Goal

Validate, normalize, and compile a dataframe request without opening an Arango
result cursor. Give the frontend actionable output before Preview.

### Owner and files

**Owner:** validation worker.

Owned files:

- new `internal/dataframe/validation.go`
- new `internal/dataframe/validation_test.go`
- additive types in `internal/dataframe/builder_types.go`
- additive service methods in `internal/dataframe/service.go`
- new `graphqlapi/dataframe/validation.go`
- new `graphqlapi/dataframe/validation_test.go`

Coordinator owns GraphQL schema, resolver, and output mapping.

### Internal contract

Add one service operation that shares preparation and compilation with Run:

```go
func (s *Service) Validate(ctx context.Context, req ValidateRequest) (ValidationResult, error)
```

The result should include:

- `Valid`;
- normalized public request or normalized builder DTO;
- root row grain and stable root identity field;
- ordered output columns;
- bounded pivot-expanded columns when known;
- warnings;
- compiler plan diagnostics;
- request fingerprint used by WP5 cursor paging;
- limits/capabilities such as preview allowed and export allowed.

Do not expose raw rendered AQL by default. A development-only diagnostic may
return its hash and plan facts.

### Implementation

1. Refactor the existing `prepareSpec` and `CompileRequest` call sequence into a
   shared internal method used by Validate, Run, and Stream.
2. Validation must perform the same:
   - project authorization;
   - active generation resolution;
   - auth-resource-path intersection;
   - populated field/reference validation;
   - pivot-column expansion and bounding;
   - semantic and physical compilation.
3. Validation must never call `ExecuteQueryRows`, create an Arango cursor, or
   profile the query.
4. Return the exact ordered columns that preview/export will produce. Include
   flattened pivot columns based on bounded columns rather than waiting to see
   runtime object keys.
5. Define warning codes for valid but potentially surprising shapes:
   - no selected data columns;
   - template suggestion unavailable;
   - high traversal fanout;
   - truncated distinct values;
   - truncated pivot candidates;
   - preview limit capped;
   - export recommended instead of preview.
6. Define a deterministic request fingerprint from canonical normalized
   semantics, project, generation, resolved scope mode/paths, output ordering,
   and compiler-relevant request values. Do not hash arbitrary JSON map order.
7. The fingerprint must exclude the page cursor and page size, but include
   filters and row grain.
8. Add configurable preview/export cost policy as a service dependency with
   conservative defaults. Start with structural facts already available in
   plan diagnostics; do not pretend Arango estimates are measured runtime.
9. Keep validation idempotent: validating an already-normalized request yields
   the same normalized result and fingerprint.
10. `runFhirDataframe`, future preview, and export must fail if their prepared
    fingerprint differs from a supplied validated fingerprint.

### Tests

- valid root-only request;
- nested traversal, aggregate, slice, and pivot request;
- unknown fieldRef and unavailable populated field;
- unsafe/unknown traversal;
- unbounded pivot;
- unauthorized project and restricted-empty scope;
- active generation changes between requests;
- Validate never invokes the execution dependency;
- Validate and Run produce identical columns and plan diagnostics;
- canonical equivalent inputs produce the same fingerprint;
- changed field, filter, scope, generation, traversal, or pivot column changes
  the fingerprint;
- deterministic warning and column ordering.

### Acceptance

- The frontend can validate every editable request without executing AQL.
- Validation uses the production compiler, not a reduced validator.
- No execution cursor is opened.
- The GDC request validates in under 100 ms warm, excluding a deliberately cold
  catalog cache.
- Existing Run and Stream behavior remains unchanged.

### Handoff

Report normalized DTO, fingerprint algorithm/version, warning schema, cost
policy defaults, benchmarks, and GraphQL requirements.

---

## WP4 — Structured frontend error contract

### Goal

Give GraphQL, preview, and export one stable error taxonomy that the frontend
can map to controls and user messages.

### Owner and files

**Owner:** error-contract worker.

Owned files:

- new `internal/dataframe/errors.go`
- new `internal/dataframe/errors_test.go`
- new `graphqlapi/errors.go`
- new `graphqlapi/errors_test.go`
- additive reusable HTTP error mapping in `internal/httpapi/errors.go`
- new `internal/httpapi/errors_test.go`

Coordinator integrates the GraphQL error presenter in `graphqlapi/handler.go`
and reconciles `internal/httpapi/server.go`.

### Error shape

Define an interface or typed error carrying:

```go
type UserError interface {
    error
    Code() string
    FieldPath() []string
    Details() map[string]any
    Retryable() bool
}
```

Do not expose internal errors, AQL text, bind variables, collection names,
filesystem paths, or stack traces through `Details`.

Minimum stable codes:

- `PROJECT_REQUIRED`
- `ROOT_RESOURCE_TYPE_REQUIRED`
- `UNAUTHORIZED_PROJECT`
- `UNKNOWN_FIELD`
- `FIELD_NOT_POPULATED`
- `INVALID_TRAVERSAL`
- `UNSAFE_TRAVERSAL_ROUTE`
- `INVALID_FILTER`
- `UNBOUNDED_PIVOT`
- `INVALID_PIVOT_COLUMN`
- `INVALID_SLICE`
- `PLAN_TOO_EXPENSIVE`
- `INVALID_CURSOR`
- `STALE_CURSOR`
- `DATASET_GENERATION_CHANGED`
- `UNSUPPORTED_EXPORT_FORMAT`
- `CLIENT_CANCELED`
- `BACKEND_UNAVAILABLE`
- `INTERNAL_ERROR`

### Implementation

1. Inventory public errors emitted by GraphQL input resolution, dataframe
   preparation, catalog validation, semantic lowering, cursor decode, and
   export adapters.
2. Wrap errors at their semantic owner. Do not classify by parsing error text
   in the GraphQL layer.
3. Preserve `errors.Is`/`errors.As` behavior and root causes for server logs.
4. Return GraphQL errors with extensions:

   ```json
   {
     "code": "UNKNOWN_FIELD",
     "fieldPath": ["rootFields", "2", "fieldRef"],
     "retryable": false,
     "details": {"fieldRef": "Patient.missing"},
     "requestId": "..."
   }
   ```

5. Reuse the same code/message/detail mapper for REST export errors before
   response streaming begins.
6. Once streaming has started, log the structured error and terminate the
   stream; do not attempt to replace a partial CSV/NDJSON body with JSON.
7. Map context cancellation separately from backend timeout/unavailability.
8. Unknown errors become `INTERNAL_ERROR` externally and retain the full cause
   only in structured server logs.
9. Document which codes are user-correctable, retryable, or operator failures.
10. Add a compatibility test preventing accidental code renames.

### Tests

- every minimum code is unique and documented;
- GraphQL extensions contain code/path/request ID;
- REST envelope uses the same code;
- internal AQL and bind values are redacted;
- wrapped errors preserve `errors.Is`/`errors.As`;
- cancellation, deadline, and Arango connection failures map differently;
- validation locates root field, nested field, pivot, filter, and traversal
  errors at the corresponding input path;
- unknown panic/error returns a generic external message.

### Acceptance

- The frontend never needs to parse an English message to identify an error.
- The same semantic failure has the same code across validation, preview, and
  export.
- Existing request IDs remain available.
- No sensitive query or scope data leaks.

### Handoff

Report the frozen code registry, Go interface, GraphQL extension shape, REST
shape, redaction rules, and coordinator integration steps.

---

## WP5 — Stable preview paging

### Goal

Replace the unused GraphQL cursor field with keyset paging over the stable root
grain. Preview pages must not use offsets and must not repeat or skip roots
within one immutable dataset generation.

### Owner and files

**Owner:** paging worker.

Owned files:

- new `internal/dataframe/cursor.go`
- new `internal/dataframe/cursor_test.go`
- new `internal/dataframe/paging.go`
- new `internal/dataframe/paging_test.go`
- additive semantic/physical paging fields in coordinator-approved dataframe
  files only;
- new `graphqlapi/dataframe/preview.go`
- new `graphqlapi/dataframe/preview_test.go`

The paging worker must stop and request coordinator approval before editing
shared physical files such as `physical_plan.go`, `physical_render.go`, or
`generic_physical_plan.go`.

Coordinator owns GraphQL schema and generated files.

### Cursor contract

Use an opaque, versioned, authenticated token. Minimum payload:

```go
type CursorV1 struct {
    Version int
    RequestFingerprint string
    Project string
    DatasetGeneration string
    RootResourceType string
    LastRootKey string
}
```

Sign the canonical payload with HMAC-SHA-256 using server configuration. Do not
accept unsigned base64 JSON in production. Provide an explicit insecure test
codec only in tests.

### Implementation

1. Add a preview request with `first` and `after`; cap `first` using configured
   minimum/default/maximum values, initially 1/25/1,000.
2. Decode and verify the cursor before compiling AQL.
3. Recompute WP3's request fingerprint and reject a cursor from a different
   request, root type, project, scope, or generation.
4. Add the root-key predicate before root `SORT` and `LIMIT`:

   ```aql
   FILTER root._key > @after_root_key
   SORT root._key ASC
   LIMIT @page_fetch_limit
   ```

5. Fetch `first + 1` rows to calculate `hasNextPage`, then return at most
   `first` rows.
6. Construct `endCursor` from the last returned root `_key`; never derive it
   from a child resource or output field selected by the user.
7. Keep `_key` available as hidden execution metadata even if the user does not
   select it as an output column. Remove hidden metadata before delivering rows.
8. Preserve required-match filtering before paging. Optional child shaping
   remains after the root window.
9. Do not implement backward paging or offset paging in Part 5.
10. Cancel and close the Arango cursor when the request context ends. Confirm
    the current driver path does so; add explicit close behavior if required.
11. Return page info:
    - `hasNextPage`;
    - `endCursor` or null for an empty page;
    - requested/effective page size;
    - request fingerprint/version.
12. Keep `runFhirDataframe(limit:)` as a thin first-page compatibility adapter
    during migration.

### Tests

- empty, one-row, exact-page, and page-plus-one datasets;
- multiple pages concatenate to the same ordered rows as one unpaged request;
- no duplicate or skipped root keys;
- required filter applied before page window;
- optional child fanout does not alter page boundaries;
- invalid signature, malformed token, unsupported version;
- cursor from another project, scope, generation, root type, or request;
- generation activation between pages produces `STALE_CURSOR` or
  `DATASET_GENERATION_CHANGED`;
- page size zero, negative, and above maximum;
- cancellation closes cursor resources;
- root `_key` is not leaked unless requested as a column;
- live META paging for the GDC shape with exact concatenated result hash.

### Acceptance

- The GraphQL cursor field is no longer decorative.
- Page concatenation has exact parity with the equivalent stable unpaged
  result.
- Explain uses the scoped root index and keyset predicate with no full scan.
- Page memory is bounded by effective page size, not total result size.
- Cursor tampering and stale generations fail with structured WP4 errors.

### Handoff

Report cursor wire version, key rotation/configuration decision, fingerprint
dependency, AQL/Explain evidence, parity hashes, and GraphQL page DTO.

---

## WP6 — Streaming NDJSON and CSV export transport

### Goal

Expose the existing validated `dataframe.Service.Stream` path as an
authenticated, cancellation-aware HTTP export without buffering the full
dataframe in GraphQL or server memory.

This is synchronous streaming export only. Durable jobs, artifact storage,
resume, and Elasticsearch are separate future work.

### Owner and files

**Owner:** export worker.

Owned files:

- new `internal/export/types.go`
- new `internal/export/ndjson.go`
- new `internal/export/csv.go`
- new `internal/export/ndjson_test.go`
- new `internal/export/csv_test.go`
- new `internal/httpapi/dataframe_export.go`
- new `internal/httpapi/dataframe_export_test.go`

Coordinator owns shared HTTP config/routes and server command wiring.

### Request and response contract

Proposed request:

```json
{
  "format": "ndjson",
  "dataframe": { "...": "FhirDataframeInput-equivalent JSON" },
  "validatedFingerprint": "optional fingerprint returned by WP3"
}
```

Response behavior:

- NDJSON: `Content-Type: application/x-ndjson; charset=utf-8`;
- CSV: `Content-Type: text/csv; charset=utf-8`;
- attachment filename is sanitized and contains project, root type, and a UTC
  timestamp, never an auth path;
- `Cache-Control: no-store`;
- `X-Content-Type-Options: nosniff`;
- optional safe headers for request fingerprint and generation;
- no `Content-Length` requirement for streaming responses.

### Implementation

1. Define a small sink interface receiving ordered columns and flattened rows.
2. NDJSON writes one canonical JSON object and one newline per row.
3. CSV writes the header once using validated ordered columns, then writes every
   value with RFC 4180-compatible quoting through `encoding/csv`.
4. Define deterministic CSV encoding:
   - null -> empty field;
   - string/number/bool -> scalar text;
   - arrays/objects -> compact canonical JSON in one CSV cell;
   - timestamps remain the compiler-delivered string representation.
5. Never derive CSV column order from Go map iteration. Use WP3's compiled
   ordered columns, including bounded flattened pivot columns.
6. Call the same GraphQL input preparation/fieldRef resolution and
   `dataframe.Service.Stream` path. Extract a transport-neutral request adapter
   if necessary; do not duplicate GraphQL-only parsing rules.
7. Resolve authorization and active generation before writing HTTP headers so
   validation failures can return a normal structured error response.
8. If `validatedFingerprint` is supplied, require exact equality with the
   freshly prepared request.
9. Flush periodically or per configured row batch. Do not flush every cell.
10. Stop promptly when the request context is canceled or the client
    disconnects; propagate cancellation to Arango.
11. Record rows and bytes written in structured logs. Do not log row contents.
12. Enforce configurable export row and duration limits for synchronous mode.
    Return `PLAN_TOO_EXPENSIVE` before streaming if the request requires a
    durable job that Part 5 does not provide.
13. Do not write temporary files, artifact metadata, job collections, or
    Elasticsearch documents in this package.
14. Register `POST /api/v1/dataframes/export` behind the existing authentication
    middleware and project authorization contract.

### Tests

- NDJSON exact rows and newline behavior;
- CSV header order, commas, quotes, newlines, Unicode, nulls, arrays, objects,
  and pivot-expanded columns;
- preview and both export formats decode to identical logical rows;
- large fake stream proves bounded memory;
- writer failure and client cancellation stop upstream execution;
- error before headers returns WP4 JSON envelope;
- error after streaming starts terminates and logs without appending JSON error
  material to CSV/NDJSON;
- unsupported `Accept`/format returns `UNSUPPORTED_EXPORT_FORMAT`;
- unauthorized project and restricted-empty scope;
- generation changes and validated-fingerprint mismatch;
- live META GDC export at 1,000 rows with row/hash parity;
- response headers prevent caching and content sniffing.

### Acceptance

- 100,000 generated test rows export with memory bounded independently of row
  count.
- Live 1,000-row GDC NDJSON and CSV exports contain the same logical rows as
  preview.
- Disconnect/cancellation stops Arango work.
- No GraphQL JSON buffering is used for export.
- No durable-job or Elasticsearch scaffolding is introduced.

### Handoff

Report sink interface, CSV value rules, endpoint request/headers, cancellation
evidence, memory benchmark, row hashes, and coordinator wiring steps.

---

## Parallel execution plan

### Phase 0 — Coordinator contract freeze

One coordinator performs a read-only audit and freezes:

- GraphQL semantic field names;
- shared DTOs between GraphQL and REST input adapters;
- WP4 error interface and initial code list;
- WP3 normalized-request and fingerprint boundary;
- shared-file hashes and worker ownership.

No generated GraphQL edit occurs yet.

### Phase 1 — Four parallel workers

These packages can start concurrently after Phase 0:

| Lane | Work | May edit |
| --- | --- | --- |
| A | WP1 catalog dataset summaries | catalog read files and dataset DTO/service files |
| B | WP2 pure template registry | `internal/dataframe/template` only initially |
| C | WP3 validation core | dataframe validation/service files |
| D | WP4 error taxonomy | new error files and tests |

Coordination rules:

- Lane B uses fake availability inputs until WP1's response interface lands.
- Lane C returns typed internal errors compatible with Lane D's frozen
  interface. If the interface is insufficient, stop and request a coordinator
  decision.
- No lane edits GraphQL schema/generated files.
- WP1 and WP3 must not independently refactor authorization or active generation
  resolution; they reuse existing services.

### Phase 2 — Two parallel workers plus integration

After WP3's normalized request/fingerprint contract and WP4 errors are merged:

| Lane | Work | Dependency |
| --- | --- | --- |
| E | WP5 cursor codec and paging | WP3 + WP4 |
| F | WP6 export sinks and handler | WP3 + WP4; may use current Stream immediately |
| Coordinator | WP1-WP4 GraphQL schema integration | stable service DTOs |

WP5 and WP6 may run concurrently because paging owns compiler/page execution
and export owns streaming formats/HTTP adapter. They must not both edit shared
dataframe service files; any shared extraction is coordinator-owned.

### Phase 3 — Single coordinator integration

The coordinator:

1. integrates service dependencies into the server command;
2. edits `graphqlapi/schema.graphqls` once for WP1-WP5;
3. runs `make generate-graphql` once;
4. wires resolvers and output mapping;
5. wires the WP6 HTTP route/configuration;
6. updates README and Quickstart examples;
7. resolves API naming and generated-code conflicts;
8. runs the complete Part 5 test matrix.

### Phase 4 — Parallel verification

After integration, three read-mostly verification workers may run concurrently:

- authorization/generation isolation across all six operations;
- GraphQL/REST contract and error compatibility;
- live META performance, paging parity, streaming memory, and cancellation.

Only the coordinator fixes shared production files. Verification workers add
focused tests/evidence or propose patches.

## Dependency graph

```text
Phase 0 contract freeze
  ├── WP1 dataset discovery ───────┐
  ├── WP2 template registry ───────┼── GraphQL integration
  ├── WP3 validate/fingerprint ─┬──┤
  └── WP4 structured errors ────┤  │
                                ├── WP5 preview paging ─┐
                                └── WP6 export ─────────┼── final integration
                                                       └── system verification
```

WP2's availability adapter depends on WP1 and existing introspection, but its
registry can be built independently. WP5 and WP6 require the final WP3/WP4
contracts. Export does not depend on paging.

## Coordinator merge order

Merge in this order even if workers finish differently:

1. WP4 internal error contract;
2. WP3 validation and fingerprint contract;
3. WP1 dataset summary service;
4. WP2 template registry and availability adapter;
5. WP5 paging;
6. WP6 export;
7. GraphQL generation and server wiring;
8. docs and system evidence.

This order prevents WP3/WP5/WP6 from inventing incompatible error or
fingerprint representations.

## Required system tests

Add a Part 5 system suite covering one complete user flow:

1. load or use the checked-in META dataset;
2. list visible datasets and resource types;
3. list templates;
4. select an available template and starter request;
5. introspect fields and distinct filter values;
6. validate the request;
7. preview at least two pages;
8. concatenate pages and verify row identity/order;
9. export NDJSON and CSV;
10. decode both exports and prove logical row parity;
11. repeat with restricted and restricted-empty scopes;
12. activate a different generation and prove the old cursor/fingerprint is
    rejected.

The system suite must include:

- Patient-root GDC matrix with nested traversals, aggregates, slices, and
  Observation pivot;
- a non-Patient root;
- a dataset where at least one of the six templates is partial/unavailable;
- malformed input and cursor cases;
- cancellation during streaming export.

## Final acceptance gate

Part 5 is complete when:

1. The frontend needs no hardcoded project resource types for the first screen.
2. Six guided template families are returned with data-backed availability.
3. Every starter request is accepted by the production validator or explicitly
   marked unavailable.
4. Validation opens no result cursor and returns stable columns, warnings,
   diagnostics, and fingerprint.
5. Every user-correctable failure has a stable structured code and input path.
6. Preview cursor paging has exact concatenation parity and stable root order.
7. NDJSON and CSV stream through the production dataframe service with bounded
   memory and cancellation.
8. Preview/export preserve project, generation, and auth-scope isolation.
9. Existing GDC result hashes and compiler conformance remain unchanged.
10. No frontend-specific FHIR route or AQL logic enters the compiler.
11. No saved-recipe, durable-job, artifact, or Elasticsearch scaffolding is
    added.
12. README and Quickstart contain copy-pasteable calls for dataset discovery,
    template selection, validation, paged preview, and both export formats.

## Luna worker prompt template

```text
Execute WP<N> from docs/LUNA_FRONTEND_ENABLEMENT_PART_5.md.

Read the entire Part 5 plan, then every file listed under that WP. Own only the
exact paths assigned to the WP. Do not edit GraphQL schema/generated files,
server wiring, shared physical compiler files, README, or Quickstart unless you
are the designated coordinator.

Preserve all global contracts, especially project/generation/auth isolation,
stable fieldRef behavior, exact preview/export row semantics, deterministic
ordering, and no FHIR/AQL logic in the frontend layer. Reuse fhirschema,
resolveStorageRoute, catalog discovery, dataframe.Service, and
dataset.ActiveManifestResolver. Never hardcode a fixture route in generic code.

Record baseline hashes. Implement only this package, run the named unit/live
tests, and produce the required handoff. Report changed files, test commands,
API/type decisions, hashes, latency/memory metrics, rejected approaches, and
coordinator decisions required.

Stop rather than guess if a shared contract must change, an owned file changed
concurrently, authorization semantics differ, active-generation semantics are
unclear, result parity fails, or a generated/shared file edit is required.
```

