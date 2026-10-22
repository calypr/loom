# Builder contract proof proposals

Status: generated review queue. These proposals do not change the route ledger.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 11 |
| contractTested | 8 |
| modeledOnly | 0 |
| unproven | 3 |
| readLike | 8 |
| stateful | 3 |
| high | 0 |
| medium | 1 |
| low | 10 |

## Review queue

| Tier | Route | Mode | Proposed status | Confidence | Browser | Test | Evidence |
| ---: | --- | --- | --- | --- | --- | --- | --- |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | read | contract-tested | low | none | none | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | stateful | unproven | medium | none | TestGeneratedExplorerMutationRoutesForwardAuthResourcePath | go:TestGeneratedExplorerMutationRoutesForwardAuthResourcePath internal/server/openapi_explorer_write_scope_test.go, go:TestOpenAPIDocumentsOperationSpecificFailureStatuses internal/server/openapi_route_inventory_test.go |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | read | contract-tested | low | none | none | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-resolutions` | stateful | unproven | low | none | none | none |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | read | contract-tested | low | none | none | none |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}/explicit-groups` | stateful | unproven | low | none | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | read | contract-tested | low | none | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | read | contract-tested | low | none | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-field-choices` | read | contract-tested | low | none | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | read | contract-tested | low | none | none | none |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-capabilities` | read | contract-tested | low | none | none | none |

## Bounds

A proposal is evidence, not proof. Review the test or browser case before adding it to `builder-route-overrides.json`.
