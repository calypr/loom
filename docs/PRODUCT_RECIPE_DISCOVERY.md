# Product Recipes and Dataset Discovery

This document describes a product direction for turning freshly loaded FHIR
data into useful flat dataframes without asking users to understand FHIRPath,
GraphQL, AQL, or graph traversal mechanics.

The central product idea is:

> The user describes a familiar dataset, Loom turns that intent into a validated
> recipe, and the backend compiles the recipe into performant AQL.

The frontend should not be an unrestricted visual query builder. It should be a
small, guided interface backed by dataset-aware analysis queries and a stable
recipe contract.

## 1. Target User Experience

The primary flow should read like a short conversation.

### What are you making?

Start from a curated use case:

- Patient cohort
- Specimen inventory
- File manifest
- Diagnoses
- Labs or observations
- Study enrollment

This choice establishes a recipe family. A recipe family supplies sensible
defaults, supported row grains, known FHIR relationships, common columns, and
backend planning hints. It is not a saved AQL string.

### One row per...

Choose the meaning of one output row:

- Patient
- Specimen
- File
- Observation

The row grain is the most important semantic choice in the workflow. It
determines which resource anchors the output, which relationships are safe to
traverse, and whether related values should be selected, aggregated, pivoted,
or emitted as additional rows.

The frontend should use the phrase "one row per" rather than "root resource"
or "traversal root."

### Include...

Present a searchable, plain-language column checklist:

- show common, populated columns first
- group columns by familiar concepts rather than raw resource names
- display a short example value when safe and useful
- show approximate coverage, such as "present for 92% of patients"
- hide advanced FHIR paths and internal graph identifiers by default
- explain when a selection changes the row grain or creates multiple values

The column picker should be populated from Loom's analysis API. It should not
be a hardcoded copy of the theoretical FHIR schema.

### Only include...

Build filters as guided sentences, for example:

- diagnosis is melanoma
- specimen type is Primary Tumor
- file type is BAM
- observation name is Hemoglobin
- study is TCGA-LUAD

The available operators and values should come from field metadata and bounded
value-frequency queries. Free text should be used only when the field cannot
provide a useful enumerated value list.

### Preview, then deliver

After validation, the user can:

- preview a small sample
- download flat NDJSON
- download CSV
- send the rows to Elasticsearch
- save the configuration as a reusable recipe

Preview and export are different workloads. Preview should be synchronous,
small, and optimized for rapid correction. Export should eventually be a
streaming or asynchronous job with durable status and bounded memory.

## 2. The Product Contract Is a Recipe

The frontend, a future natural-language assistant, the CLI, and API clients
should all produce the same versioned recipe object.

A conceptual recipe looks like this:

```json
{
  "version": 1,
  "template": "file_manifest",
  "project": "example-project",
  "grain": "file",
  "columns": [
    "cap_patient_submitter_id",
    "cap_specimen_type",
    "cap_file_name",
    "cap_file_type",
    "cap_file_size"
  ],
  "filters": [
    {
      "field": "cap_file_type",
      "operator": "equals",
      "value": "BAM"
    }
  ],
  "destination": {
    "type": "preview"
  }
}
```

This is intentionally not the current GraphQL dataframe input and not AQL.
The `cap_*` values are illustrative opaque capability identifiers issued by
Loom; a browser never constructs a FHIR path from them. It is a product-level
intermediate representation.

The recipe compiler is responsible for translating friendly concepts into:

1. concrete FHIR resource types and fields
2. a supported relationship path
3. field selections, filters, aggregates, pivots, and representative slices
4. the existing dataframe builder request
5. the lowered internal plan
6. AQL and bind variables

Keeping these layers separate has several benefits:

- the frontend remains small
- recipes can be saved and migrated
- a future LLM only needs to emit a constrained object
- validation can return user-facing errors before compiling AQL
- planner improvements do not invalidate saved user intent
- the same recipe can target preview, file export, or Elasticsearch

## 3. Why Dataset Analysis Comes First

FHIR defines what may exist. A newly loaded dataset determines what actually
exists, how it is connected, and whether it is useful.

Loom already profiles populated fields in `fhir_field_catalog` and discovers
populated references from `fhir_edge`. These are necessary primitives, but the
product needs higher-level answers.

The analysis layer must build on the repository's existing generated FHIR
backbone: `schemas/graph-fhir.json`, generated Go structs/validators/edge
extractors under `internal/fhir`, generated metadata under `internal/fhirschema`,
and the sample data in `META/`. It should not introduce a second FHIR model or
infer schema structure independently from those owners.

The frontend should not call a collection of unrelated low-level queries and
attempt to infer the product model itself. Loom should expose analysis results
that already use the same semantics and authorization rules as recipe
validation and compilation.

### Implemented foundation

`dataframebuilder.Service.DiscoverGuided` now composes the existing scoped
catalog readers into an internal `discovery.Snapshot`. The snapshot contains
only generated-schema roots, compiler-safe observed relationships, opaque
candidate-column IDs, and bounded guided filter suggestions. It deliberately
does not yet have a public GraphQL/HTTP endpoint. When configured with an
active-manifest resolver, it is bound to that active dataset generation before
catalog discovery so cached/persisted capabilities cannot be used outside the
scope or load they describe.

The lifecycle vocabulary is persisted by `internal/datasetstore` (schema
snapshot, immutable generation reference, lifecycle state, and active pointer),
and generation-aware discovery/compiler requests propagate that selection. It
is still not a public discovery response or a finalized analysis-capability
snapshot.

The first internal capability bridge is also implemented:
`internal/recipecompiler.Build` and
`dataframebuilder.Service.PrepareRecipe`/`RunRecipe`/`StreamRecipe` resolve
opaque IDs from fresh, authorization-scoped catalog facts into typed root-only
scalar dataframe selections and filters. That makes an internal preview/row
stream possible today while rejecting raw paths, stale IDs, cross-resource
choices, repeated fields without a quantifier, pivots, and pinned generations.
It does not yet make a saved recipe, relationship-aware template, download, or
Elasticsearch delivery product-supported.

## 4. Required Analysis Query Families

Analysis queries should be project-scoped and auth-resource-path-scoped. Their
results should be cacheable and invalidated when a load changes the project.

### 4.1 Dataset summary

Purpose: determine what kind of dataset was loaded.

Return:

- populated resource types and document counts
- available auth resource paths
- total edge counts
- field-catalog freshness/version
- load or analysis timestamp
- high-level warnings, such as resources with no usable references

This powers the initial landing state and prevents the frontend from offering
recipe families that have no backing data.

### 4.2 Relationship inventory

Purpose: understand which FHIR resource types are actually linked.

Return for each observed relationship:

- source resource type
- relationship label
- target resource type
- edge count
- distinct source count
- distinct target count
- source coverage
- average and percentile fanout
- whether the relationship is recognized by Loom's semantic planner

The current populated-reference discovery returns the relationship and edge
count. Product discovery additionally needs coverage and fanout. An edge count
alone cannot tell the UI whether a relationship is nearly universal or a rare
outlier, nor whether it will multiply output rows unexpectedly.

### 4.3 Reachable recipe paths

Purpose: expose useful, supported paths rather than arbitrary graph walks.

Given a row grain or recipe family, return paths such as:

- Patient to Specimen
- Patient to DocumentReference
- Patient to Specimen to DocumentReference
- Patient to Condition
- Patient to Observation
- Patient to ResearchSubject to ResearchStudy

Each path should include:

- friendly label
- concrete edge labels and resource types
- observed coverage
- estimated fanout
- supported planner operations
- support state: supported, preview-only, experimental, or unavailable
- reason when unavailable

This endpoint must reflect the planner's real capabilities. A path is not
product-supported merely because matching edges exist.

### 4.4 Candidate columns

Purpose: populate the plain-language column checklist.

Given a recipe family, grain, and optional path, return:

- stable friendly field identifier
- display label and description
- owning concept/resource
- underlying canonical selector
- data kind
- document count and coverage percentage
- bounded example values
- cardinality estimate
- whether values are scalar, repeated, or complex
- allowed value modes
- whether aggregation or pivoting is required at the selected grain
- common, suggested, or advanced classification
- sensitivity or display restrictions when applicable

The existing field catalog supplies much of the raw evidence. The analysis
layer adds grain-aware presentation and planner compatibility.

### 4.5 Filter suggestions and value frequencies

Purpose: power guided filter sentences.

Given a candidate field, return:

- supported operators
- common distinct values and counts
- approximate or exact frequency indicator
- truncation state
- null/missing count
- search over high-cardinality values

The existing catalog's bounded distinct values are useful for initial hints.
They are not sufficient for every filter UI because they do not necessarily
represent value frequency or support searching a high-cardinality domain.

Expensive value analysis should be requested on demand, bounded, cached, and
subject to the same authorization scope as dataframe execution.

### 4.6 Grain and cardinality analysis

Purpose: explain what one output row means before execution.

Given a proposed recipe, estimate:

- number of base entities at the selected grain
- expected output rows
- which selections can multiply rows
- which selections produce arrays
- which selections will be aggregated or pivoted
- expected null coverage
- whether a stable document identifier can be produced

This is both a correctness feature and a user-experience feature. Many failed
dataframe requests are really misunderstandings about cardinality.

### 4.7 Recipe validation and explain

Purpose: provide one authoritative preflight operation.

Given a recipe, return:

- normalized recipe
- validation status
- user-facing warnings and errors
- selected relationship path
- output column names and types
- estimated cardinality
- planner support state
- a safe logical-plan explanation
- whether preview, export, and Elasticsearch delivery are available

The explain response should not expose raw AQL by default. Raw GraphQL, AQL,
and bind variables can remain available in a developer-only diagnostics view.

### 4.8 Preview

Purpose: let the user validate meaning with real rows.

Preview should:

- use the normalized recipe returned by validation
- enforce a small maximum row count
- return stable columns and representative rows
- report elapsed time and relevant warnings
- avoid retaining an unbounded result in memory
- make missing values and repeated values visually understandable

### 4.9 Export and destination readiness

Purpose: determine whether a validated recipe can be delivered safely.

For file export, check:

- output format
- expected size
- stable column schema
- whether the query can stream

For Elasticsearch, additionally check:

- destination/index permissions
- index or index-template compatibility
- field-name and type conflicts
- deterministic document ID strategy
- bulk batch limits
- behavior for partial failures and retries

Elasticsearch delivery should consume the same row stream as NDJSON/CSV export;
it should not introduce a second dataframe compiler.

## 5. Query Templates vs. Capabilities

It is useful to create reusable backend query templates, but they should be
organized as capabilities rather than frontend pages or conversational turns.

For example:

| Capability | Likely backing data | Product consumer |
| --- | --- | --- |
| Dataset summary | resource collections, catalog, edges | recipe gallery |
| Relationship inventory | `fhir_edge` | grain/path selection |
| Candidate columns | `fhir_field_catalog`, schema metadata | column picker |
| Filter values | catalog plus bounded live aggregation | filter sentence |
| Recipe explain | catalog, semantics, planner | preflight panel |
| Preview | compiled dataframe AQL | result table |
| Export | compiled dataframe AQL row stream | files/Elasticsearch |

The conversation is then a client of these capabilities. This prevents UI
wording from becoming part of the database API and makes it possible to add a
CLI, alternate frontend, or LLM later.

## 6. Recommended API Shape

The exact GraphQL names can be decided during implementation, but the public
surface should conceptually support:

```graphql
datasetSummary(project: ID!): DatasetSummary!
recipeTemplates(project: ID!): [RecipeTemplate!]!
recipeOptions(input: RecipeContextInput!): RecipeOptions!
fieldValueSuggestions(input: FieldValueSuggestionInput!): FieldValuePage!
validateRecipe(input: DataframeRecipeInput!): RecipeValidation!
previewRecipe(input: DataframeRecipeInput!, limit: Int!): DataframePreview!
startRecipeExport(input: DataframeRecipeInput!, destination: ExportDestinationInput!): ExportJob!
```

This does not require seven unrelated implementations. Several operations can
compose the current catalog readers, generated FHIR schema, derived field
references, traversal rules, and dataframe planner. The formal gap analysis
proposes consolidating those current semantics behind one registry.

Avoid making the frontend assemble raw `FhirTraversalStepInput` trees. That
contract remains useful as a lower-level developer API, but it is too close to
the compiler for the primary product experience.

## 7. Recipe Template Definition

A recipe template should declare intent and constraints, not contain canned
AQL.

Each template should define:

- stable template ID and version
- user-facing name and description
- supported row grains
- required and optional semantic relationships
- suggested columns
- optional default filters
- supported aggregates and pivots
- planner capability requirements
- expected identifier strategy
- supported destinations

Initial templates should be deliberately small:

### Patient cohort

- default grain: Patient
- common columns: submitter ID, sex/gender, vital status, study
- common filters: diagnosis, study, demographics

### Specimen inventory

- default grain: Specimen
- common columns: patient ID, specimen ID/type, collection metadata
- common filters: specimen type, study, diagnosis

### File manifest

- default grain: File/DocumentReference
- common columns: patient ID, specimen ID/type, file name/type/size/access
- common filters: file type, specimen type, study

### Diagnoses

- default grain: diagnosis/Condition
- common columns: patient ID, diagnosis, stage, age/date fields
- common filters: diagnosis, stage, study

### Labs or observations

- default grain: Observation
- common columns: patient ID, observation code/name, value, unit, date
- common filters: observation code, status, date range

### Study enrollment

- default grain: ResearchSubject or patient-study membership
- common columns: patient ID, study ID/name, enrollment status, arm
- common filters: study and status

These definitions are hypotheses until exercised against representative loaded
datasets and the actual planner.

## 8. Implementation Sequence

### Phase 1: capture conversations as fixtures

Write 10 to 20 realistic user requests and expected recipe objects. Include
ambiguous and impossible requests.

Examples:

- "Give me a file manifest for BAM files with patient and specimen type."
- "One row per patient with melanoma diagnosis and all studies."
- "Show hemoglobin observations with value, unit, and collection date."

These fixtures become the acceptance contract for the frontend and any future
language interface.

### Phase 2: inventory current evidence

For each fixture, record whether the current catalog can answer:

- is the recipe family present?
- is the requested grain available?
- is the required path populated?
- are requested columns populated?
- are requested filter values discoverable?
- will the planner lower the request?

This identifies missing analysis queries using evidence rather than guesswork.

### Phase 3: add analysis services

Start with:

1. dataset summary
2. relationship coverage/fanout
3. grain-aware candidate columns
4. filter values/frequencies
5. recipe validation/explain

Prefer extending the existing catalog and dataframebuilder services over adding
raw AQL strings directly to HTTP handlers.

### Phase 4: add versioned recipes and templates

Implement recipe types, normalization, semantic identifiers, template
definitions, and translation into the existing dataframe builder.

### Phase 5: build the thin guided frontend

The frontend should call the analysis and recipe APIs. It should contain little
FHIR-specific logic beyond display grouping and state management.

### Phase 6: add streaming export

Build NDJSON and CSV from one row-stream abstraction, then add Elasticsearch as
another destination over the same stream.

## 9. Acceptance Criteria for the First Product Slice

The first slice is complete when a non-technical user can:

1. open a freshly loaded project
2. see only recipe families supported by its data and planner
3. select a row grain using plain language
4. choose suggested populated columns
5. create a filter from observed values
6. understand warnings about missing data or row multiplication
7. preview meaningful rows
8. save and reload the recipe
9. download flat NDJSON or CSV

Direct Elasticsearch delivery can follow once the export stream and schema
validation are reliable.

## 10. Design Guardrails

- Do not expose arbitrary graph traversal as the default experience.
- Do not equate "edge exists" with "planner supports this recipe."
- Do not equate "FHIR field is legal" with "field is populated."
- Do not make the frontend calculate coverage, fanout, or planner support.
- Do not store AQL as the durable user artifact.
- Do not let an LLM emit or execute raw AQL.
- Do not return unbounded exports inline through GraphQL.
- Do keep project and authorization scope identical across analysis, preview,
  and export.
- Do preserve a developer diagnostics view for recipe, logical plan, GraphQL,
  AQL, and bind-variable inspection.

## 11. First Concrete Work Package

The recommended first implementation work package is a gap-analysis harness,
not a new frontend.

It should contain:

- representative conversation fixtures
- expected normalized recipes
- expected row grain and columns
- catalog/discovery evidence captured for each fixture
- planner validation result
- preview correctness result
- missing-capability classification

That harness will show whether the next investment belongs in catalog analysis,
FHIR semantics, planner coverage, AQL performance, or user experience. It also
becomes the regression suite that keeps the frontend honest as Loom expands.
