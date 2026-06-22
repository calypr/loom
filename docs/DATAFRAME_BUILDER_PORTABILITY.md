# FHIR-Native GraphQL Dataframe Builder Draft

## Summary

This document defines a draft GraphQL contract for the future dataframe-builder
read API. The contract is intentionally **FHIR-native** and mirrors the mental
model already proven out by the Arango prototype:

1. choose a root FHIR resource type and auth scope,
2. traverse through actual populated FHIR reference labels,
3. attach field selectors directly to each traversed node,
4. attach pivot operations directly to the node that owns the pivotable field,
5. execute either a preview or an export.

This is not a generic graph-builder schema. It is a builder contract for the
specific graph and field surfaces already present in this repo:

- populated references discovered from `fhir_edge`, as implemented in
  [internal/proto/discovery.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/discovery.go:9)
- populated canonical field paths and pivot metadata discovered at load time in
  [internal/proto/field_catalog.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog.go:17)
- traversal and field-plucking behavior demonstrated by the live
  `gdc_case_assay_matrix` AQL in
  [queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:1)

GraphQL is the user-facing contract. AQL remains the likely execution target
underneath for the Arango-backed implementation.

## Source Model in the Current Prototype

### Populated Traversals

The traversal surface is already represented in `fhir_edge` as:

- `from_type`
- `label`
- `to_type`
- `edge_count`

That is the exact shape returned by
[internal/proto/discovery.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/discovery.go:42),
which currently groups edges by `from_type`, `label`, and `to_type`.

Example logical edges already used by the case-assay query:

- `Patient <-subject_Patient- Specimen`
- `Specimen <-member_entity_Specimen- Group`
- `Specimen <-subject_Specimen- DocumentReference`
- `Group <-subject_Group- DocumentReference`
- `Patient <-subject_Patient- Condition`
- `Patient <-subject_Patient- ResearchSubject`
- `Patient <-subject_Patient- MedicationAdministration`

Those traversals are visible directly in
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:13),
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:47),
and
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:55).

### Populated Fields

The field catalog already records observed canonical FHIR paths per
`project/resource_type/path`, including:

- `path`
- `kind`
- `doc_count`
- `distinct_values`
- `distinct_truncated`
- `pivot_candidate`
- `pivot_kind`
- `pivot_columns`

This is the stored shape of
`FieldCatalogDocument` in
[internal/proto/field_catalog.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog.go:29).

The path walker canonicalizes arrays with bracket wildcards such as:

- `identifier[].value`
- `code.coding[].display`
- `valueCodeableConcept`
- `valueCodeableConcept.coding[].display`

That behavior is implemented by `walkShapeValue`, `appendPath`, and
`extractAccessorValues` in
[internal/proto/field_catalog.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog.go:362),
and tested in
[internal/proto/field_catalog_test.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog_test.go:7).

### Pivot Semantics

V1 pivot semantics already exist in the load-time field profiler:

- only `CodeableConcept`-shaped objects are pivot candidates
- pivot kind is `codeable_concept_display_value`
- candidate pivot columns come from observed coding displays

The current implementation marks a field as a pivot candidate when
`classifyObjectShape` detects a `CodeableConcept`, via
[internal/proto/field_catalog.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog.go:439).
Observed pivot columns are accumulated by `addPivotColumn` in
[internal/proto/field_catalog.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog.go:249).
The expected behavior is tested in
[internal/proto/field_catalog_test.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog_test.go:92).

This contract draft keeps those semantics intact. It does not invent generalized
pivoting beyond what the prototype already observes.

## Contract Goals

The GraphQL contract should let the frontend express:

- what resource type is the root driver,
- which auth-scoped projects are included,
- which traversals are followed,
- which FHIR fields are plucked from each node,
- which fields are pivoted,
- whether the user wants a small preview or an export artifact.

The contract should **not** force nested GraphQL resolvers to perform per-hop
fanout. The contract describes the dataframe shape; the backend remains free to
compile that request into one backend query plan or one staged export pipeline.

## Draft SDL

```graphql
scalar JSON
scalar DateTime

enum FhirPivotKind {
  CODEABLE_CONCEPT_DISPLAY_VALUE
}

enum DataframeRunMode {
  PREVIEW
  EXPORT
}

input FhirDataframeBuilderInput {
  project: String!
  authResourcePaths: [String!]!
  rootResourceType: String!
  fields: [FhirFieldSelectInput!]!
  pivots: [FhirPivotSelectInput!] = []
  traversals: [FhirTraversalStepInput!] = []
}

input FhirTraversalStepInput {
  label: String!
  toResourceType: String!
  alias: String!
  fields: [FhirFieldSelectInput!]!
  pivots: [FhirPivotSelectInput!] = []
  traversals: [FhirTraversalStepInput!] = []
}

input FhirFieldSelectInput {
  name: String!
  select: String!
}

input FhirPivotSelectInput {
  name: String!
  select: String!
  pivotKind: FhirPivotKind = CODEABLE_CONCEPT_DISPLAY_VALUE
  columns: [String!]
}

input FhirDataframeRunInput {
  builder: FhirDataframeBuilderInput!
  mode: DataframeRunMode! = EXPORT
  previewLimit: Int = 25
}

type FhirPopulatedReference {
  fromType: String!
  label: String!
  toType: String!
  edgeCount: Int!
}

type FhirPopulatedField {
  resourceType: String!
  path: String!
  kind: String!
  docCount: Int!
  distinctValues: [String!]!
  distinctTruncated: Boolean!
  pivotCandidate: Boolean!
  pivotKind: String
  pivotColumns: [String!]!
}

type FhirDataframePreview {
  columns: [String!]!
  rows: [JSON!]!
  rowCount: Int!
}

type FhirDataframeExportHandle {
  exportId: String!
  status: String!
  format: String!
}

type FhirDataframeRunResult {
  mode: DataframeRunMode!
  preview: FhirDataframePreview
  export: FhirDataframeExportHandle
}

input FhirBuilderMetadataInput {
  project: String!
  authResourcePaths: [String!]!
  rootResourceType: String!
  fromResourceType: String
  resourceType: String
  pivotOnly: Boolean = false
}

type Query {
  fhirPopulatedReferences(input: FhirBuilderMetadataInput!): [FhirPopulatedReference!]!
  fhirPopulatedFields(input: FhirBuilderMetadataInput!): [FhirPopulatedField!]!
}

type Mutation {
  runFhirDataframe(input: FhirDataframeRunInput!): FhirDataframeRunResult!
}
```

## Selector Expression Syntax

Each selected field uses a compact FHIR-aware extraction expression through the
`select` string. This is intentionally smaller than a full query language.

### Path Form

Supported path style in the draft:

- `identifier[].value`
- `identifier[0].value`
- `extension[].valueString`
- `code.coding[].display`
- `valueCodeableConcept`
- `valueCodeableConcept.coding[].display`

Array behavior is explicit in the selector:

- `[]` means iterate the array
- `[0]` means address the first item explicitly

The contract does not rely on hidden flattening behavior outside the selector.

### Predicate Form

The selector may narrow values by sibling-field predicates, for example:

- `identifier[].value where system contains "case_id"`
- `identifier[].value where system contains "case_submitter_id"`
- `extension[].valueString where url contains "us-core-race"`
- `category[].coding[].display where system contains "data_category"`
- `category[].coding[].display where system contains "experimental_strategy"`

The intent is to match the style already used in the current AQL, such as
filtering identifiers by `system` and extensions by `url`, visible in
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:7)
and
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:90).

This selector syntax is a frontend contract, not a commitment to a specific
parser implementation yet.

## Builder Inspection Queries

Builder inspection is how the frontend learns what is actually populated in the
current dataset, rather than relying only on the full FHIR schema.

### Populated Traversals

```graphql
query BuilderReferences($input: FhirBuilderMetadataInput!) {
  fhirPopulatedReferences(input: $input) {
    fromType
    label
    toType
    edgeCount
  }
}
```

Example variables:

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "authResourcePaths": ["EllrottLab-GDC_Data"],
    "rootResourceType": "Patient",
    "fromResourceType": "Patient"
  }
}
```

This is a GraphQL wrapper over the existing discovery surface implemented by
`discover-populated-references`.

### Populated Fields

```graphql
query BuilderFields($input: FhirBuilderMetadataInput!) {
  fhirPopulatedFields(input: $input) {
    resourceType
    path
    kind
    docCount
    distinctValues
    distinctTruncated
    pivotCandidate
    pivotKind
    pivotColumns
  }
}
```

Example variables:

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "authResourcePaths": ["EllrottLab-GDC_Data"],
    "rootResourceType": "Observation",
    "resourceType": "Observation",
    "pivotOnly": true
  }
}
```

This is a GraphQL wrapper over the existing field catalog read path and should
ultimately map to `discover-populated-fields`, including the current
`--pivot-only` behavior already wired in
[cmd/arango-fhir-proto/main.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/cmd/arango-fhir-proto/main.go:269).

## Execution Example: Case-Assay Dataframe

This example is intentionally modeled on the current
`gdc_case_assay_matrix` AQL recipe.

```graphql
mutation RunCaseAssayMatrix($input: FhirDataframeRunInput!) {
  runFhirDataframe(input: $input) {
    mode
    export {
      exportId
      status
      format
    }
  }
}
```

Example variables:

```json
{
  "input": {
    "mode": "EXPORT",
    "builder": {
      "project": "ARANGODB_PROTO",
      "authResourcePaths": ["EllrottLab-GDC_Data"],
      "rootResourceType": "Patient",
      "fields": [
        {
          "name": "case_id",
          "select": "identifier[].value where system contains \"case_id\""
        },
        {
          "name": "case_submitter_id",
          "select": "identifier[].value where system contains \"case_submitter_id\""
        },
        {
          "name": "gender",
          "select": "gender"
        },
        {
          "name": "race",
          "select": "extension[].valueString where url contains \"us-core-race\""
        },
        {
          "name": "ethnicity",
          "select": "extension[].valueString where url contains \"us-core-ethnicity\""
        },
        {
          "name": "birth_sex",
          "select": "extension[].valueCode where url contains \"us-core-birthsex\""
        },
        {
          "name": "patient_age",
          "select": "extension[].valueQuantity.value where url contains \"Patient-age\""
        }
      ],
      "pivots": [],
      "traversals": [
        {
          "label": "subject_Patient",
          "toResourceType": "Specimen",
          "alias": "specimen",
          "fields": [
            {
              "name": "specimen_type",
              "select": "type.coding[].display"
            },
            {
              "name": "preservation_method",
              "select": "processing[].method.coding[].display where system contains \"preservation_method\""
            }
          ],
          "pivots": [],
          "traversals": [
            {
              "label": "member_entity_Specimen",
              "toResourceType": "Group",
              "alias": "group",
              "fields": [],
              "pivots": [],
              "traversals": [
                {
                  "label": "subject_Group",
                  "toResourceType": "DocumentReference",
                  "alias": "group_file",
                  "fields": [
                    {
                      "name": "group_file_data_category",
                      "select": "category[].coding[].display where system contains \"data_category\""
                    },
                    {
                      "name": "group_file_data_type",
                      "select": "category[].coding[].display where system contains \"data_type\""
                    },
                    {
                      "name": "group_file_experimental_strategy",
                      "select": "category[].coding[].display where system contains \"experimental_strategy\""
                    },
                    {
                      "name": "group_file_workflow_type",
                      "select": "category[].coding[].display where system contains \"workflow_type\""
                    }
                  ],
                  "pivots": []
                }
              ]
            },
            {
              "label": "subject_Specimen",
              "toResourceType": "DocumentReference",
              "alias": "specimen_file",
              "fields": [
                {
                  "name": "specimen_file_data_category",
                  "select": "category[].coding[].display where system contains \"data_category\""
                },
                {
                  "name": "specimen_file_data_type",
                  "select": "category[].coding[].display where system contains \"data_type\""
                },
                {
                  "name": "specimen_file_experimental_strategy",
                  "select": "category[].coding[].display where system contains \"experimental_strategy\""
                },
                {
                  "name": "specimen_file_workflow_type",
                  "select": "category[].coding[].display where system contains \"workflow_type\""
                }
              ],
              "pivots": []
            }
          ]
        },
        {
          "label": "subject_Patient",
          "toResourceType": "DocumentReference",
          "alias": "patient_file",
          "fields": [
            {
              "name": "patient_file_data_category",
              "select": "category[].coding[].display where system contains \"data_category\""
            },
            {
              "name": "patient_file_data_type",
              "select": "category[].coding[].display where system contains \"data_type\""
            },
            {
              "name": "patient_file_experimental_strategy",
              "select": "category[].coding[].display where system contains \"experimental_strategy\""
            },
            {
              "name": "patient_file_workflow_type",
              "select": "category[].coding[].display where system contains \"workflow_type\""
            }
          ],
          "pivots": []
        },
        {
          "label": "subject_Patient",
          "toResourceType": "Condition",
          "alias": "condition",
          "fields": [
            {
              "name": "diagnosis_id",
              "select": "identifier[].value where system contains \"diagnosis_id\""
            },
            {
              "name": "primary_diagnosis",
              "select": "code.coding[].display"
            },
            {
              "name": "diagnosis_body_site",
              "select": "bodySite[].coding[].display"
            }
          ],
          "pivots": []
        },
        {
          "label": "subject_Patient",
          "toResourceType": "ResearchSubject",
          "alias": "research_subject",
          "fields": [
            {
              "name": "research_subject_id",
              "select": "id"
            },
            {
              "name": "research_subject_status",
              "select": "status"
            }
          ],
          "pivots": []
        },
        {
          "label": "subject_Patient",
          "toResourceType": "MedicationAdministration",
          "alias": "treatment",
          "fields": [
            {
              "name": "treatment_category",
              "select": "category[].coding[].display"
            }
          ],
          "pivots": []
        }
      ]
    }
  }
}
```

This example intentionally does not attempt to encode every rollup detail from
the current AQL. The contract expresses the traversal and field-selection
intent. Backend compilation can still produce counts, representative arrays, and
aggregated booleans in the final exported dataframe shape.

## Pivot Example

The draft must keep pivot operations first-class because the load-time field
catalog already detects pivotable `CodeableConcept` fields.

For example, the current tests verify that `valueCodeableConcept` may be marked
as:

- `pivot_candidate = true`
- `pivot_kind = codeable_concept_display_value`
- `pivot_columns` containing observed coding displays

That exact behavior is tested in
[internal/proto/field_catalog_test.go](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/internal/proto/field_catalog_test.go:92).

Example GraphQL mutation:

```graphql
mutation RunObservationPivot($input: FhirDataframeRunInput!) {
  runFhirDataframe(input: $input) {
    mode
    preview {
      columns
      rows
      rowCount
    }
  }
}
```

Example variables:

```json
{
  "input": {
    "mode": "PREVIEW",
    "previewLimit": 10,
    "builder": {
      "project": "ARANGODB_PROTO",
      "authResourcePaths": ["EllrottLab-GDC_Data"],
      "rootResourceType": "Observation",
      "fields": [
        {
          "name": "observation_id",
          "select": "id"
        },
        {
          "name": "observation_code",
          "select": "code.coding[].display"
        }
      ],
      "pivots": [
        {
          "name": "observation_value_codeable_concept",
          "select": "valueCodeableConcept",
          "pivotKind": "CODEABLE_CONCEPT_DISPLAY_VALUE",
          "columns": [
            "American Joint Committee on Cancer pM0"
          ]
        }
      ],
      "traversals": []
    }
  }
}
```

Interpretation:

- the selected field `valueCodeableConcept` is not flattened as a plain scalar
- instead, the builder declares that this field should be pivoted using the
  currently supported `CodeableConcept` pivot semantics
- the candidate columns come from the observed `pivot_columns` metadata

This stays aligned with the current implementation rather than inventing a new
pivot engine.

## Multi-Path Auth Scope

The current AQL prototype filters on a singular `@auth_resource_path`, for
example in
[queries/gdc_case_assay_matrix_arango_rows.aql](/Users/peterkor/Desktop/BMEG/ARANGODB_PROTO/queries/gdc_case_assay_matrix_arango_rows.aql:1).
The GraphQL contract broadens this to:

```graphql
authResourcePaths: [String!]!
```

Execution semantics in the draft are:

- all rows are still scoped to one logical `project`
- the caller may include multiple authorized resource paths
- the effective visibility scope is the union of those paths

This is a contract-level requirement for the future API. It does not require the
current prototype to already accept multiple paths in the same backend query.

## Preview vs Export

The contract separates two execution modes:

- `PREVIEW`
- `EXPORT`

### Preview

Preview exists only to support the builder UX:

- quick sanity check on selected traversals and fields
- small row sample
- not the primary delivery surface for large dataframe output

### Export

Export is the primary execution mode:

- returns an export handle
- avoids pushing large dataframe payloads inline through GraphQL
- leaves room for async materialization and file delivery

This keeps the contract compatible with the actual workload shape, which is a
graph-to-table export rather than a small nested object read.

## Acceptance Criteria

This draft is correct if:

- it uses real FHIR resource types already present in the prototype
- it uses real reference labels already present in `fhir_edge`
- it uses canonical observed-style field paths
- it includes pivot operations explicitly
- it supports multiple `authResourcePaths`
- it reads as “build this dataframe” rather than “inspect a generic graph”
- it stays compatible with compiling to one backend query plan instead of
  requiring GraphQL resolver fanout

## Assumptions and Defaults

- V1 pivot support remains limited to current `CodeableConcept` detection.
- `alias` remains the stable traversal-hop identifier.
- Field selection uses compact selector expressions instead of low-level
  extraction knobs.
- GraphQL is the builder and read contract; backend-native query languages
  remain the execution substrate.
- Export remains the primary mode; preview exists to support the builder UI.
