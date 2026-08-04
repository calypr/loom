# Quickstart

This guide gets a fresh local checkout to:

1. ArangoDB running in Docker
2. sample FHIR data loaded
3. the GraphQL server running at `http://127.0.0.1:8080`
4. Apollo Sandbox working at `http://127.0.0.1:8080/apollo`
5. a real dataframe query returning rows

## 1. Start ArangoDB

The lightweight local compose file is:

- [`experimental/docker-compose.yml`](../experimental/docker-compose.yml)

Bring it up:

```bash
rtk docker compose -f experimental/docker-compose.yml up -d
```

This starts:

- ArangoDB at [http://127.0.0.1:8529](http://127.0.0.1:8529)

If you also want the research/benchmark databases, the larger stack is:

- [`experimental/docker/docker-compose.full.yml`](../experimental/docker/docker-compose.full.yml)

## 2. Generate code and build binaries

```bash
make generate-fhir
make generate-graphql
make build
```

This produces:

- [`bin/arango-fhir-proto`](../bin/arango-fhir-proto)
- [`bin/arango-fhir-server`](../bin/arango-fhir-server)

## 3. Load the bundled sample dataset

The repo ships a local sample FHIR dataset under [`META/`](../META).

Load it into the default local database:

```bash
./bin/arango-fhir-proto load \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --meta-dir META \
  --project ARANGODB_PROTO \
  --auth-resource-path EllrottLab-GDC_Data
```

What this does:

- bootstraps one collection per FHIR resource type
- bootstraps `fhir_edge`
- bootstraps `fhir_field_catalog`
- loads raw payloads and generated edges
- profiles populated fields for builder introspection

The bootstrap and collection definitions live in
[`internal/ingest/backend.go`](../internal/ingest/backend.go).

## 4. Start the HTTP server

For local work, run with `--no-auth`:

```bash
./bin/arango-fhir-server \
  --listen :8080 \
  --no-auth \
  --url http://127.0.0.1:8529 \
  --database fhir_proto \
  --dataframer-recipe /path/to/dataframer.json
```

Useful endpoints:

- Apollo Sandbox: [http://127.0.0.1:8080/apollo](http://127.0.0.1:8080/apollo)
- GraphQL endpoint: [http://127.0.0.1:8080/graphql/graph](http://127.0.0.1:8080/graphql/graph)
- FHIR dataframe endpoint: [http://127.0.0.1:8080/graphql/dataframe](http://127.0.0.1:8080/graphql/dataframe)
- Published dataframe fields: [http://127.0.0.1:8080/graphql/graph](http://127.0.0.1:8080/graphql/graph)
- Health check: [http://127.0.0.1:8080/health](http://127.0.0.1:8080/health)

On macOS you can jump straight to Apollo with:

```bash
open http://127.0.0.1:8080/apollo
```

## 5. Run a builder introspection query

Use this first. It tells you what traversals and fields are actually populated
for the chosen root resource type.

To promote a proven interactive query into the server's persistent default,
follow the [dataframer recipe authoring guide](DATAFRAMER_RECIPES.md).

### Query

```graphql
query BuilderIntrospection($input: DataframeBuilderIntrospectionInput!) {
  dataframeBuilderIntrospection(input: $input) {
    project
    rootResourceType
    authResourcePaths
    root {
      resourceType
      fields {
        fieldRef
        label
        path
        selector {
          sourcePath
          valuePath
          where {
            path
            op
            value
          }
        }
      }
      pivotFields {
        fieldRef
        label
        pivotFamily
        pivotColumns
        defaultPivotColumnSelector {
          sourcePath
          valuePath
        }
        defaultPivotValueSelector {
          sourcePath
          valuePath
        }
      }
      traversals {
        fromType
        label
        toType
        edgeCount
      }
    }
    relatedResources {
      viaLabel
      edgeCount
      target {
        resourceType
        fields {
          fieldRef
          label
          path
        }
      }
    }
  }
}
```

### Variables

```json
{
  "input": {
    "project": "ARANGODB_PROTO",
    "rootResourceType": "Patient",
    "includePivotOnlyFields": true
  }
}
```

## 6. Run a sample dataframe query

This is the current mutation shape:

```graphql
mutation RunDataframe($input: FhirDataframeInput!, $limit: Int) {
  runFhirDataframe(input: $input, limit: $limit) {
    columns
    rowCount
    rows
  }
}
```

Use these variables for a meaningful patient-first dataframe:

```json
{
  "limit": 100,
  "input": {
    "project": "ARANGODB_PROTO",
    "rootResourceType": "Patient",
    "rootFields": [
      {
        "name": "case_id",
        "fieldRef": "Patient.case_id",
        "fallbackFieldRefs": [
          "Patient.case_submitter_id"
        ],
        "valueMode": "AUTO"
      },
      {
        "name": "case_submitter_id",
        "fieldRef": "Patient.case_submitter_id",
        "valueMode": "AUTO"
      },
      {
        "name": "gender",
        "fieldRef": "Patient.gender",
        "valueMode": "AUTO"
      },
      {
        "name": "birth_sex",
        "fieldRef": "Patient.birth_sex",
        "valueMode": "AUTO"
      }
    ],
    "traverse": [
      {
        "edgeLabel": "subject_Patient",
        "toResourceType": "Condition",
        "alias": "condition",
        "aggregates": [
          {
            "name": "condition_count",
            "operation": "COUNT"
          },
          {
            "name": "diagnosis_values",
            "operation": "DISTINCT_VALUES",
            "fhirPath": "code.coding[].display",
            "valueMode": "AUTO"
          }
        ],
        "slices": [
          {
            "name": "representative_conditions",
            "limit": 2,
            "fields": [
              {
                "name": "condition_id",
                "selector": {
                  "valuePath": "id"
                },
                "valueMode": "AUTO"
              },
              {
                "name": "diagnosis",
                "selector": {
                  "sourcePath": "code.coding[]",
                  "valuePath": "display"
                },
                "valueMode": "AUTO"
              }
            ]
          }
        ]
      },
      {
        "edgeLabel": "subject_Patient",
        "toResourceType": "Specimen",
        "alias": "specimen",
        "aggregates": [
          {
            "name": "specimen_count",
            "operation": "COUNT"
          },
          {
            "name": "specimen_types",
            "operation": "DISTINCT_VALUES",
            "fhirPath": "type.coding[].display",
            "valueMode": "AUTO"
          }
        ],
        "slices": [
          {
            "name": "representative_specimens",
            "limit": 3,
            "fields": [
              {
                "name": "specimen_id",
                "selector": {
                  "valuePath": "id"
                },
                "valueMode": "AUTO"
              },
              {
                "name": "specimen_type",
                "selector": {
                  "sourcePath": "type.coding[]",
                  "valuePath": "display"
                },
                "valueMode": "AUTO"
              }
            ]
          }
        ]
      },
      {
        "edgeLabel": "subject_Patient",
        "toResourceType": "ResearchSubject",
        "alias": "research_subject",
        "aggregates": [
          {
            "name": "research_subject_count",
            "operation": "COUNT"
          }
        ],
        "slices": [
          {
            "name": "representative_research_subjects",
            "limit": 2,
            "fields": [
              {
                "name": "research_subject_id",
                "selector": {
                  "valuePath": "id"
                },
                "valueMode": "AUTO"
              },
              {
                "name": "status",
                "selector": {
                  "valuePath": "status"
                },
                "valueMode": "AUTO"
              },
              {
                "name": "study_ref",
                "fieldRef": "ResearchSubject.study_ref",
                "valueMode": "AUTO"
              }
            ]
          }
        ]
      }
    ]
  }
}
```

Notes:

- `rows` is returned as JSON, not as statically typed GraphQL row objects
- the server validates selectors/traversals against populated-field and populated-reference discovery
- `fieldRef` is the preferred frontend-friendly path when available

### Run it and see the timing

The checked-in operation and variables above are also runnable without copying
JSON from this document. With the local server running in `--no-auth` mode:

```bash
make dataframe-demo
```

This prints the actual GraphQL response plus wall-clock time. To issue the
same request repeatedly and see min/average/max timing:

```bash
make dataframe-demo DATAFRAME_REPEAT=10
```

The command labels the first request as `cold` and the final request as
`warm`, then reports total HTTP/server time, returned rows, response bytes, and
rows/second. It also reports server-side field-reference resolution, request
preparation, physical compilation, Arango cursor time, per-row materialization,
and result assembly. The remaining wall-clock time is GraphQL serialization and
HTTP overhead.

The compact example files are
[`examples/meta_patient_dataframe.graphql`](../examples/meta_patient_dataframe.graphql)
and
[`examples/meta_patient_dataframe.variables.json`](../examples/meta_patient_dataframe.variables.json).
`make dataframe-demo` runs the richer GDC-style case matrix in
[`examples/meta_gdc_case_matrix.graphql`](../examples/meta_gdc_case_matrix.graphql)
with [`examples/meta_gdc_case_matrix.variables.json`](../examples/meta_gdc_case_matrix.variables.json):
diagnoses, specimens, nested files and sample groups, Observation code/value
pivots, and representative related records.
For an explicit named GDC operation, run:

```bash
rtk go run ./cmd/dataframe-query \
  -query examples/meta_gdc_case_matrix.graphql \
  -variables examples/meta_gdc_case_matrix.variables.json \
  -repeat 1
```

## 7. Shut down local Arango

```bash
rtk docker compose -f experimental/docker-compose.yml down
```
