# Compiler-Era Cleanup Audit

This audit asks a narrower question than `go tool deadcode`: which package
tracks no longer belong in Loom now that dataframe requests have a real,
generation-aware FHIR compiler? It traces production call sites and storage
contracts as of this checkout. It does not treat a new compiler foundation as
dead merely because it has not yet reached the final product transport.

## Runtime authority

The executing dataframe path is:

```text
POST /graphql
  -> graphqlapi resolver
  -> dataframebuilder.Service.Run
  -> dataframe.Service
  -> semantic validation / Lower
  -> compileLowered
  -> Arango QueryRows or Stream
```

The current compiler, catalog, generated FHIR metadata, loader, and Arango
client all remain live core. Generic lowering is real execution code; the
physical-plan renderer is currently an Explain/diagnostic foundation, not its
replacement.

## Removed in this cleanup

| Track | Why it was removed | Evidence |
| --- | --- | --- |
| Hard-coded GDC AQL CLI/exporter | It bypassed compiler validation and generation scope. Its default query performed unqualified document lookup and had no `dataset_generation` predicate. It was not an Elasticsearch delivery implementation. | The only command entries were `query-gdc-case-assay-matrix` and `export-gdc-case-assay-matrix`; the only integration dependency was its own raw-AQL assertion. |
| `queries/*.aql` | The files encoded GDC-specific rows, old `project::id` keys, or alternate edge collections which current ingest neither creates nor reads. | No generic compiler or catalog call site used them. |
| `/builder` browser page | The 1,062-line page hard-coded a GDC project, fields, pivots, and an eight-item edge-label map, then submitted raw graph-editor inputs. It did not build requests from compiler-safe catalog capabilities. | Its only production entry was `GET /builder`. |
| `patient_file_rollup` bootstrap collection | No loader wrote it and no compiler, query, or reader consumed it. Creating its indexes at every bootstrap advertised a materialization that did not exist. | References were limited to bootstrap, its bootstrap test, and docs. Existing operator-created collections are deliberately not dropped by this code cleanup. |
| `fhir_scalar_index` constant | It had no bootstrap, writer, reader, or test. | The declaration was its only reference. |

The Docker image no longer copies manual AQL files. The legacy integration test
now verifies load/catalog behavior rather than an obsolete query recipe.

## Package decisions

| Area | Decision | Reason |
| --- | --- | --- |
| `cmd/arango-fhir-proto` | Keep as an operator surface, simplify | It now owns loading and catalog diagnostics only; the hard-coded GDC query/export commands were removed. |
| `cmd/arango-fhir-server` | Keep | It wires the compiler, catalog cache, authorization, GraphQL, and optional active-generation resolver. |
| `internal/api` | Keep, remove only the demo page | It is the HTTP host and principal-propagation boundary. The one-file import route is a separate compatibility decision. |
| `internal/graphqlapi` and generated model | Keep temporarily; stop extending | This is the live compiler transport. It should be replaced by the guided product contract in one deliberate schema/codegen cutover, not deleted piecemeal. |
| `internal/authscope` | Keep | It is the runtime authorization-scope contract used by catalog discovery and compiler execution. |
| `internal/store/arango` | Keep | It is the only executing backend and owns native query, lifecycle, and Explain operations. |
| `internal/fhir`, `internal/fhirschema`, `cmd/generate` | Keep | They are generator-owned FHIR/schema authority and support every active root. |
| `internal/ingest`, `fhir_edge`, `fhir_field_catalog` | Keep | They are the actual write path and compiler evidence layer. Do not prune traversal/index strategy without new Explain coverage. |
| `internal/catalog` and cache | Keep | They supply scoped observed fields, values, pivots, and relationship facts to both current GraphQL and guided discovery. |
| `internal/datasetstore` and `schemaidentity` | Keep | Immutable manifest/pointer lifecycle and exact schema identity are on the generation-aware read/write path. |
| `internal/dataset` read-binding helpers | Defer a focused decision | Some value types are only unit-tested while runtime reads use `authscope.ReadScope`; they are new lifecycle work, not an old compiler track. |
| `internal/dataframe` generic lowering | Keep and extend | It is the live compiler execution path for general requests. |
| Specialized Patient/case-assay lowering | Retain behind explicit deletion gates | It still changes semantics and shares sibling traversals more aggressively than generic lowering. |
| `internal/dataframe` physical plan/renderer | Retain as incomplete new compiler work | It is navigation-only Explain/diagnostic IR, not a stale pre-compiler implementation. Do not describe it as an execution replacement until it renders all semantic operations. |
| `internal/dataframebuilder` and current GraphQL AST | Keep temporarily; stop extending | They are the only public execution transport, but expose raw graph-editor concepts that conflict with the guided product contract. |
| `internal/discovery`, `recipe`, `recipecompiler`, `export`, `dataframeexport` | Keep as product foundations | They are intentionally not yet publicly wired; they are not duplicate AQL implementations. |
| Mutable CLI load and one-file HTTP import | Keep temporarily; do not extend | They remain reachable compatibility paths. Generation-mode server already rejects the HTTP route. Delete only with a complete bundle/job replacement. |
| `experimental/docker-compose.yml` | Keep | It is the tracked local Arango development runtime. |

## Compiler correctness found during audit

Generic lowering had one semantic inconsistency: an `AUTO` selection of a
repeated related field was documented as `FIRST` but rendered as a sorted unique
array. The generic lowering path now lowers it as deterministic `FIRST` and
sorts the related set by `_key`; the pre-existing specialized Patient behavior
is left unchanged pending its dedicated migration. Focused value-mode tests
cover both `AUTO` and `FIRST` traversal selection.

## Do not delete the specialized Patient lowerer yet

Deleting `planner.go`'s Patient/case-assay branch, `traversal_rules.go`, or
`document_reference_semantics.go` today would remove real behavior, not dead
code. The deletion requires all of the following:

1. A generic sibling-prefix sharing pass that traverses the common Patient
   neighbor prefix once, then applies resource-type subsets. The specialized
   plan currently shares this work across Condition, Specimen, Observation,
   and related children; generic sharing only recognizes identical target
   types.
2. Explicit, authorization-scoped outbound lowering for
   `ResearchSubject -> study -> ResearchStudy`. Current generic storage-route
   validation correctly rejects the forward stored edge; the legacy lookup is
   an implicit workaround, not an equivalent generic route.
3. Result-shape and Arango Explain/cost parity for representative patient,
   specimen/file, DocumentReference, and study-enrollment fixtures.
4. An explicit product decision about GDC-style DocumentReference summaries.
   That special logic chooses `content[0]`, applies code/display fallbacks,
   and emits GDC classifications. If it is needed, it belongs in a named
   manifest/recipe capability rather than generic FHIR lowering.

Only then should the old set kinds, special traversal registry, lookup logic,
and legacy conformance fixtures be removed together.

## Next cleanup boundaries

These items are deliberately deferred rather than silently removed:

- Current GraphQL exposes `cursor` and `selectionHint` that the input mapper
  does not use, while the compiler has typed filters and required traversal
  matching that GraphQL cannot express. Fix this in one transport cutover:
  publish guided capabilities/recipe preview, then remove the raw GraphQL
  graph-editor contract and regenerate gqlgen.
- The direct pre-lowered `Builder` surface contains legacy set/derived
  operation variants and nested traversal fields with no runtime producer.
  Encapsulate it after conformance fixtures compile request-level plans rather
  than construct lowered internals directly; this avoids deleting a test-only
  escape hatch while it is still a de facto API.
- `shaping.go` and `shaping_plan.go` are currently test-only normalization
  helpers whose policies are not consumed by the executing renderer. Either
  connect them to semantic lowering or remove/quarantine them in the same
  lowered-IR encapsulation pass.
- `internal/dataset` has a few read-binding/fingerprint helpers used only by
  their own tests, while live reads use `authscope.ReadScope` and
  `datasetstore.ResolveActiveManifest`. They are newly added lifecycle
  foundations, so decide whether to integrate or prune them in a dedicated
  lifecycle pass rather than mixing that decision into removal of old code.

## Verification expectation

After any cleanup touching compiler behavior, run:

```bash
GOCACHE=$(pwd)/.gocache GOTOOLCHAIN=auto go test ./...
make conformance
make compiler-bench
```

For traversal/index changes, also run the opt-in local Arango Explain gate from
[`COMPILER_IMPLEMENTATION_STATUS.md`](COMPILER_IMPLEMENTATION_STATUS.md).
