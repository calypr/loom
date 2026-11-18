# Dataframe API

The three dataframe operations use JSON POST requests under
`/api/v1/projects/{project}/dataframes`. See the
[OpenAPI contract](../api/openapi.yaml) for complete schemas and status codes.

## Page through published rows

Send this body to `/rows`. The selector identifies the recipe, translation
version, and output.

```json
{
  "selector": {"recipe": "cohort", "translationVersion": "v1", "output": "patients"},
  "columns": ["patient_id", "gender"],
  "filters": [{"column": "gender", "op": "EQ", "value": "female"}],
  "sort": {"column": "patient_id"},
  "first": 25
}
```

The response includes materialization metadata, projected columns, rows,
stable row IDs, total count, and page information. If
`pageInfo.hasNextPage` is true, pass `pageInfo.endCursor` as `after` with the
same selector, columns, filters, and sort. A changed publication or request
produces a stale cursor error.

## Aggregate a published dataframe

Send the same selector and filters to `/aggregations` with named specs. For
example, `"specs": [{"name": "gender", "kind": "TERMS", "column": "gender", "size": 10}]`
requests the most common values. The response includes materialization
metadata and the result for each spec. Both published read operations apply the
project and resource-path grants before querying ClickHouse.

## Query the active FHIR generation

Send a dataframe request body to `/run`.
Loom resolves the active generation and compiles and executes one scoped
dataframe query over ArangoDB. The response includes columns, JSON rows,
row count, and compiler and execution diagnostics. Run a saved request
with `go run ./cmd/dataframe-query -request /path/to/request.json`.
