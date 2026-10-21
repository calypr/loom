# Builder readiness

Status: generated from the route ledger, obligation report, browser evidence, trace contract, and feature map.

End to end ready: no.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 39 |
| tier1 | 21 |
| tier2 | 18 |
| strictRouteBlockers | 36 |
| routeActionAudited | 15 |
| routeActionMissing | 24 |
| routeActionEmpty | 10 |
| routeActionCandidate | 1 |
| traceTested | 1 |
| modeledAndTracedWithoutBrowser | 0 |
| browserCases | 1 |
| browserCoveredRoutes | 11 |
| ledgerRoutesWithBrowserCoverage | 11 |
| obligations | 239 |
| covered | 15 |
| partial | 86 |
| missing | 138 |
| missingRouteBound | 0 |
| missingCandidate | 102 |
| missingUnbound | 36 |
| tests | 878 |
| testsWithoutExactObligations | 866 |
| models | 13 |
| traceModels | 3 |
| schedules | 4 |
| affordances | 57 |
| modeledAffordances | 57 |
| testedAffordances | 57 |

## Route proof status

| Status | Count |
| --- | ---: |
| modeled-and-traced | 3 |
| trace-tested | 1 |
| modeled-only | 32 |
| contract-tested | 0 |
| unproven | 3 |
| excluded | 0 |

## Route action audit

| State | Count |
| --- | ---: |
| proven | 3 |
| traced | 1 |
| candidate | 1 |
| empty | 10 |
| missing | 24 |

## Missing obligations

| Binding | Count |
| --- | ---: |
| route | 0 |
| candidate | 102 |
| unbound | 36 |

## Next worklist

| Tier | Route | Status | Route action | Next gap |
| ---: | --- | --- | --- | --- |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/builder` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-proposals` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-preview` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-revisions` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-mapping` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-member-proposals` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-change` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-proposals` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections` | modeled-only | missing | audit route action |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | modeled-only | missing | audit route action |
| 2 | `GET /api/v1/projects/{project}/explorers` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers` | modeled-only | missing | audit route action |
| 2 | `DELETE /api/v1/projects/{project}/explorers/{explorerId}` | modeled-only | missing | audit route action |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/capability` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-capabilities` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | modeled-only | missing | audit route action |
| 2 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-definition-choices` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | modeled-only | missing | audit route action |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/suggestions` | modeled-only | missing | audit route action |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-proposals` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-resolutions` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}/explicit-groups` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-choices` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-inputs` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-expand-choices` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-expand-contributors` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-field-choices` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/semantic-inventory` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-capabilities` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-choice-proposals` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | unproven | missing | assign a model action or exclude |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | unproven | missing | assign a model action or exclude |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/configured-column-context` | unproven | missing | assign a model action or exclude |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-category-discoveries` | trace-tested | traced | add browser evidence |

## Blockers

- `get:api-v1-projects-project-explorers-explorerId-authoring-v2-builder` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-category-discoveries` is `trace-tested`. add browser evidence.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-choice-proposals` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-proposals` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-interpretation-preview` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-interpretation-revisions` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-population-mapping` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-population-member-proposals` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-population-routes` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-publish` is `unproven`. assign a model action or exclude.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-row-change` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-row-definition-proposals` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-category-discoveries` is `unproven`. assign a model action or exclude.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-proposals` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-resolutions` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-selections` is `modeled-only`. audit route action.
- `get:api-v1-projects-project-explorers-explorerId-selections-selectionRevision` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-selections-selectionRevision-explicit-groups` is `modeled-only`. choose contract-tested with a route test or unproven.
- `get:api-v1-projects-project-explorers` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers` is `modeled-only`. audit route action.
- `delete:api-v1-projects-project-explorers-explorerId` is `modeled-only`. audit route action.
- `get:api-v1-projects-project-explorers-explorerId-authoring-v2-capability` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-column-source` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-configured-column-context` is `unproven`. assign a model action or exclude.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-capabilities` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-choices` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-inputs` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-frame-source-options` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-related-expand-choices` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-related-expand-contributors` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-related-field-choices` is `modeled-only`. choose contract-tested with a route test or unproven.
- `get:api-v1-projects-project-explorers-explorerId-authoring-v2-row-definition-choices` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-row-lineage` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-semantic-inventory` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-suggestions` is `modeled-only`. audit route action.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-capabilities` is `modeled-only`. choose contract-tested with a route test or unproven.

## Bounds

This report ranks work. It does not prove a route. The strict route check and the full formal runner remain the gates.
