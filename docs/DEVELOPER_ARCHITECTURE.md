# Developer Architecture

Loom is an Arango-backed compiler for flat, exportable FHIR dataframes. The
authority for FHIR structure is the checked-in
[`schemas/graph-fhir.json`](../schemas/graph-fhir.json) graph schema and the
generated Go metadata derived from it. Code should not add a second handwritten
FHIR model or a parallel AQL implementation.

## Runtime surfaces

`cmd/arango-fhir-proto` is the operator CLI. Its supported commands are:

- `load` for the temporary mutable compatibility load;
- `load-generation` for a complete immutable dataset generation;
- `discover-populated-references` and `discover-populated-fields` for
  catalog diagnostics.

`cmd/arango-fhir-server` owns the HTTP process. It mounts health, GraphQL, and
developer GraphQL tools. In `--dataset-generations` mode it resolves one active
READY generation and rejects the legacy one-file HTTP import endpoint.

The GraphQL dataframe mutation is a live compatibility/expert transport to the
compiler, not the intended non-technical product UI. Do not add graph-editor
features to it. Guided discovery and recipe preparation live below transport
in `internal/dataframebuilder`, `internal/discovery`, and
`internal/recipecompiler` until the dedicated product endpoint is added.

## Load and storage model

`internal/ingest` reads an NDJSON directory, preflights it against the local
schema, builds vertices and `fhir_edge` documents, profiles populated fields,
and writes batches to Arango. Generated loaders are the fast path for covered
resources; the schema-backed generic loader is the fallback for the other
active graph roots.

Fresh loads bootstrap:

- one document collection per loaded FHIR resource type;
- `fhir_edge`, the only stored relationship representation;
- `fhir_field_catalog`, the evidence source for available fields, pivots, and
  relationships;
- `loom_dataset_lifecycle` in immutable-generation mode.

There is no maintained `patient_file_rollup`, scalar-index collection, or
alternate relationship collection. A future materialization must be introduced
as a compiler-selected physical optimization with write/read ownership,
generation scope, freshness policy, and Explain coverage; it must not be added
as an unused bootstrap collection.

Immutable loads use `internal/dataset`, `internal/datasetstore`, and
`internal/schemaidentity`. Their manifest records project, generation, and
schema identity; the active pointer selects one READY generation per project.
The generation-qualified physical keys and mandatory generation predicates are
part of the query correctness contract, not an optional filter.

## Compiler path

The current runtime call path is:

```text
GraphQL request
  -> graphqlapi resolver
  -> dataframebuilder.Service
  -> dataframe.Service
  -> semantic validation and lowering
  -> lowered AQL compiler
  -> Arango query execution/streaming
```

`internal/dataframe` owns semantics, authorization-aware query compilation,
and execution. `internal/catalog` owns scoped observed-field and relationship
facts. `internal/fhirschema` owns generated structural metadata. These
boundaries matter: catalog observations constrain what is populated, while
schema metadata constrains what a request means.

Generic lowering is the default direction. It accepts generated reverse/builder
relationship routes proven to correspond to the physical `INBOUND` `fhir_edge`
layout, plus the explicitly proven `ResearchSubject --study--> ResearchStudy`
`OUTBOUND` route. A schema-valid forward FHIR reference alone is still not
sufficient proof; every other forward route remains rejected until it has a
verified storage contract.

The older specialized Patient/case-assay lowerer is still production-reachable
for selected request shapes. It cannot be deleted merely because generic
lowering exists: it currently supplies shared sibling traversal behavior,
DocumentReference normalization, and ResearchSubject-to-ResearchStudy lookup
semantics. See [`COMPILER_CLEANUP_AUDIT.md`](COMPILER_CLEANUP_AUDIT.md) for its
explicit removal gates.

`internal/dataframe/physical_plan.go` and its renderer are a typed diagnostic
and optimization foundation. They are not yet the execution renderer for all
selections, filters, aggregates, pivots, and required relationships. Keep them
as new compiler work rather than treating their current limited runtime
reachability as dead code.

## Product foundations

The product-facing contract should exchange opaque catalog capability IDs and
recipe intent, never browser-provided FHIR selectors, graph labels, auth paths,
or AQL. Relevant ownership is:

- `internal/discovery`: scoped guided capability snapshots;
- `internal/recipe`: versioned user intent and templates;
- `internal/recipecompiler`: capability-to-typed-dataframe translation;
- `internal/export` and `internal/dataframeexport`: flat NDJSON/CSV streaming
  primitives.

These are foundations, not a claim that the product API, job system, saved
recipes, Elasticsearch delivery, or all relationship/pivot recipes are done.
Keep the boundary clean so those features can be added without reviving a
hand-maintained AQL track.

## Compatibility tracks and removal order

The following compatibility tracks remain deliberately, but should not grow:

- mutable CLI `load` and `POST /api/v1/imports`, until a complete
  generation-aware upload/job flow replaces them;
- raw-structure GraphQL dataframe input, until guided capability/recipe
  transport is public;
- specialized Patient lowering, until generic lowering has equivalent result
  and Explain/cost coverage.

The former hard-coded GDC AQL files, GDC export command, browser `/builder`
demo, and unowned bootstrap materializations were removed. They bypassed the
compiler and did not work with immutable generation keys.

## Generated code and tests

Run `make generate-fhir` after changing the graph schema and
`make generate-graphql` after changing the GraphQL schema. Do not hand-edit
generated FHIR or gqlgen output.

The normal verification targets are:

```bash
make test
make conformance
make compiler-bench
```

Opt-in Arango tests validate actual execution and Explain behavior. New query
optimizations must add result-shape tests plus Explain/cost expectations before
changing bootstrap indexes or deleting a semantic fallback.
