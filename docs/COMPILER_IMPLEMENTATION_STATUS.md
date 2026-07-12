# Compiler-First Implementation Status

This is a factual handoff/status document for the compiler-first program in
[`COMPILER_FIRST_PLAN.md`](COMPILER_FIRST_PLAN.md). It records what Loom can
actually do in this checkout, what evidence exists, and what still must not be
advertised as a finished product feature.

## Current Contract

Loom now accepts a typed dataframe request, resolves it against the checked-in
FHIR graph schema and populated catalog, and lowers supported requests into
authorization-scoped AQL. The generic path supports every concrete root type
currently generated from `schemas/graph-fhir.json`; it is not a claim of
universal support for every FHIR release or a resource not represented by that
schema.

The checked-in schema currently produces 23 concrete dataframe roots:

```text
BodyStructure, Condition, DiagnosticReport, DocumentReference,
FamilyMemberHistory, Group, ImagingStudy, Medication,
MedicationAdministration, MedicationRequest, MedicationStatement,
Observation, Organization, Patient, Practitioner, PractitionerRole,
Procedure, ResearchStudy, ResearchSubject, Specimen, Substance,
SubstanceDefinition, Task
```

The 14 cases in the optimized generated-loader dispatcher remain the fast
ingest path. The other active-schema roots use the existing generic,
schema-backed row builder. A resource type absent from the active graph schema
is deliberately rejected rather than guessed.

## Implemented Compiler Work

| Area | State in this checkout | Evidence / boundary |
| --- | --- | --- |
| Corpus and regression checks | Implemented | `conformance/compiler/` covers compiler fixtures, generated-root coverage, and benchmark entrypoints; `conformance/recipes/` freezes guided conversation examples. `make conformance` runs the suite and `make compiler-bench` runs the compiler oracle. |
| Semantic request and grain | Implemented | `SemanticPlan`, explicit root/row identity, typed selection, cardinality validation, and stable plan explanation live in `internal/dataframe/`. |
| Schema-derived roots and fields | Implemented | `cmd/generate` derives roots from the checked-in graph schema and verifies generated metadata stays fresh. No handwritten parallel FHIR model was introduced. |
| Generic FHIR graph lowering | Implemented, storage-route constrained | Generic root and traversal lowering work across the active generated root surface, while the older Patient path remains an optional specialized optimization. A related-resource route must have a compiler-owned stored-edge proof: generated reverse/builder routes map to `INBOUND`, and the explicitly proven `ResearchSubject --study--> ResearchStudy` route maps to `OUTBOUND`. Other schema-valid forward FHIR references remain rejected rather than compiled in the wrong direction. |
| Typed filters | Implemented | Root and child filters, date/date-time metadata, filter pushdown, scoped binds, and validation are represented before AQL generation. |
| Relationship existence | Implemented | `REQUIRED` traversal matches lower to bounded root-correlated semi-joins before root sorting and limiting; optional traversal behavior is retained. |
| Pivots and aggregates | Implemented | Bounded pivot shaping, stable values, and aggregate behavior have compiler tests. |
| Physical-plan contract | Partial | Typed generic physical plans, scope verification, and a navigation-only renderer exist. The renderer is not yet the execution compiler for selections, filters, pivots, aggregates, or required matches. |
| Optimizer rules | Implemented baseline | Filter pushdown, traversal sharing, and required relationship semi-join rules are explicit. Cost-based join choice and rollup selection are still future work. |
| Explain and Arango plan checks | Implemented baseline | Safe compiler explanation plus Arango `EXPLAIN` parsing/assessment are covered by unit tests and opt-in local integration tests. |

## Observed Performance Evidence

The opt-in local Arango gate runs generic lowering, required relationship
matching, specialized/generic result parity, root-preview index selection, and
the physical navigation renderer:

```bash
LOOM_COMPILER_ARANGO_INTEGRATION=1 \
  GOCACHE=$(pwd)/.gocache GOTOOLCHAIN=auto \
  go test ./internal/dataframe -run 'Test(GenericSpecimenPlanExplainsAndRunsAgainstArango|GenericRootPreviewUsesScopedSortIndexAgainstArango|RenderedGenericPhysicalNavigationExplainsAgainstArango|GenericAndSpecializedPatientPlansHaveResultParityAgainstArango|RequiredTraversalMatchExplainsAndRunsAgainstArango)' -count=1 -v
```

The observed generic traversal plan uses the native `fhir_edge` edge index on
`_to` and has no full collection scan. Root preview compilation now needs the
project-plus-stable-key access path, so bootstrap creates `project,_key` and
`project,auth_resource_path,_key` indexes for every staged resource
collection—not just Patient or DocumentReference.

That index is created at bootstrap for fresh loads. An already-loaded local
collection does not gain it until Loom bootstraps that collection again or an
operator adds the equivalent index. Do not read current local `EXPLAIN` cost
for an old collection as evidence that the new fresh-load index policy ran.

## Ingest Safety Added Alongside the Compiler

Before opening an Arango connection, `Load` now:

1. loads the configured graph schema;
2. groups staged NDJSON files by their filename resource type;
3. samples a bounded number of payloads in each file to verify
   `resourceType` and JSON shape;
4. reports generated, generic, or unsupported loader mode per resource; and
5. rejects the complete staged request before database bootstrap if any issue
   exists.

`PreflightError` preserves every structured issue for a future HTTP/CLI
presentation. Full validation still occurs while streaming the complete input,
so the bounded preflight does not pretend to validate an entire large import.

Generic ingestion now also copies the auth-resource-path scope to its generated
edges. This keeps the generic fallback subject to the same edge authorization
predicates used by lowering and catalog discovery.

`internal/schemaidentity` fingerprints the exact configured schema bytes and
exposes only source-explicit schema metadata plus this binary's generated FHIR
roots. `LoadSummary` and preflight telemetry carry that identity before Arango
is opened, and immutable generation manifests persist a defensive schema
snapshot rather than inventing a second identity representation.

`internal/datasetstore` persists the C1 lifecycle in the non-truncating
`loom_dataset_lifecycle` collection: immutable project/generation manifests,
PREFLIGHT/LOADING/ANALYZING/READY/terminal transitions, and one active pointer
per project. Activation is one guarded AQL operation that selects a READY
candidate and supersedes the prior active generation atomically.

`ingest.Load` keeps the old mutable behavior when `LoadOptions.Dataset` is nil.
With a validated dataset reference, it requires a complete nonempty directory,
runs schema and payload preflight before opening Arango, prohibits truncation,
namespaces vertex/edge physical keys by project and generation, writes
immutable graph and field-catalog documents, then activates only after all
files and catalog finalization succeed. Any row, edge, generation,
cancellation, or catalog failure leaves the manifest FAILED; a READY activation
transport failure is reported as an unknown outcome rather than falsely marking
it failed. The one-file loader intentionally rejects generation mode.

The CLI command `arango-fhir-proto load-generation --generation OPAQUE_ID`
owns that complete-directory load path. `arango-fhir-server
--dataset-generations` resolves the active READY manifest before dataframe
discovery/execution and disables the legacy one-file `/api/v1/imports` route.
There is not yet an HTTP bundle-upload or background-load endpoint.

## Thin Product Foundation Added

The product layer deliberately stays independent from FHIR paths and AQL:

- `internal/recipe` defines versioned recipe intent, opaque selected-column and
  filter IDs, destination intent, and six stable starting templates.
- `ListTemplates` and `LookupTemplate` provide the user-facing options:
  patient cohort, specimen inventory, file manifest, diagnoses,
  labs/observations, and study enrollment.
- Existing `internal/dataframebuilder.Introspect` already queries the populated
  field catalog, bounded distinct values, pivot candidates, and observed
  one-hop relationships under caller project and authorization scope. This is
  the live source for the proposed “include” and “only include” controls.
- `dataframebuilder.Service.DiscoverGuided` now composes those existing scoped
  catalog readers into `internal/discovery.Snapshot`: generated-schema roots,
  compiler-safe observed relationships, opaque candidate-column IDs, and
  bounded guided filter suggestions. It intentionally has no GraphQL/HTTP
  exposure yet. When an active-manifest resolver is configured, the snapshot,
  authorization scope, catalog facts, compiler, and execution are all pinned
  to the same immutable generation.
- `internal/recipecompiler.Build` and
  `dataframebuilder.Service.PrepareRecipe`/`RunRecipe`/`StreamRecipe` resolve
  those opaque IDs only against fresh, authorization-scoped catalog facts into
  typed **root-only scalar** dataframe plans. They can preview or stream the
  result through the existing compiler without accepting a browser-supplied
  FHIR selector, graph label, or AQL fragment. A stale, related-resource,
  repeated, pivot-only, raw-path, or pinned-generation choice is rejected
  rather than guessed.
- `internal/export` now has strict flat-row NDJSON and CSV encoders, and
  `internal/dataframeexport` connects them to `dataframe.Service.Stream`
  without collecting result rows. They remain foundations only: no artifact
  storage, jobs, public endpoint, stable generation snapshot, or Elasticsearch
  transport exists yet. Inferred CSV deliberately replays the query; explicit
  CSV columns are a single streaming execution.
- `dataframe.Service.Stream` now executes the same validated request path as a
  preview while handing flattened rows to a caller one at a time. When the
  service is configured with the active-generation resolver, it uses the same
  immutable-generation binding as preview execution. It has not yet been
  exposed as a delivery endpoint.

The root-only bridge is deliberately not a claim that every recipe template is
currently executable. Relationship columns/filters, pivots, repeated-value
quantifiers, aggregates, and recipes pinned to an immutable dataset generation
still need explicit contracts and generation-bound capability facts. The next
bridge extension must preserve the same rule: it may resolve only capabilities
emitted for the current project/generation/scope, never an arbitrary FHIR path
or AQL snippet from a browser recipe.

## Still Deliberately Incomplete

The following are planned work, not present-product claims:

- a public guided-capability API that joins template, observed catalog data,
  and compiler support reasons;
- GraphQL/REST exposure for typed filters, required relationship match modes,
  recipe creation, and safe compiler explanation;
- a full physical-plan renderer wired into execution;
- cost-based traversal/index/rollup choice and enforced scan/memory budgets;
- cursor-stable row streaming from the Arango driver;
- durable NDJSON/CSV export files, background jobs, cancellation, and retry;
- Elasticsearch transport, mapping policy, idempotency, and failure recovery;
- readiness/dependency diagnostics and production deployment controls.

## Correct Integration Order

1. Persist the next analysis/capability snapshot beside the now-bound immutable
   dataset generation, then expose it through one public guided-capability
   response.
2. Extend the existing capability-to-typed-request resolver only after its
   relationship, cardinality, and generation-binding contracts are explicit;
   make recipe validation call it before preview/export.
3. Wire the existing row result path to bounded streaming output and then add
   durable delivery jobs around that same stream.
4. Bind reusable recipes and asynchronous delivery to an active generation and
   its future analysis version; do not infer either from a mutable project.
5. Extend physical execution only when every new physical operator has result
   parity and `EXPLAIN` evidence against the generic reference path.

This order keeps a small guided UI possible without turning the browser into a
FHIR/AQL compiler or claiming support that the backend cannot prove.
