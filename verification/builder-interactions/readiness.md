# Builder readiness

Status: generated from the route ledger, obligation report, browser evidence, trace contract, and feature map.

End to end ready: no.

## Summary

| Measure | Count |
| --- | ---: |
| routes | 39 |
| tier1 | 21 |
| tier2 | 18 |
| strictRouteBlockers | 20 |
| routeActionAudited | 39 |
| routeActionMissing | 0 |
| routeActionEmpty | 28 |
| routeActionCandidate | 8 |
| traceTested | 1 |
| modeledAndTracedWithoutBrowser | 0 |
| browserCases | 1 |
| browserCoveredRoutes | 11 |
| ledgerRoutesWithBrowserCoverage | 11 |
| obligations | 259 |
| covered | 6 |
| partial | 89 |
| missing | 164 |
| missingRouteBound | 19 |
| missingCandidate | 70 |
| missingUnbound | 75 |
| tests | 905 |
| testsWithoutExactObligations | 895 |
| models | 14 |
| traceModels | 3 |
| schedules | 4 |
| affordances | 57 |
| modeledAffordances | 57 |
| testedAffordances | 57 |

## Route proof status

| Status | Count |
| --- | ---: |
| modeled-and-traced | 2 |
| trace-tested | 1 |
| modeled-only | 17 |
| contract-tested | 17 |
| unproven | 2 |
| excluded | 0 |

## Route action audit

| State | Count |
| --- | ---: |
| proven | 2 |
| traced | 1 |
| candidate | 8 |
| empty | 28 |
| missing | 0 |

## Lever inputs

| Measure | Count |
| --- | ---: |
| routeActionProposals | 0 |
| routeActionProposalLow | 0 |
| contractProofProposals | 11 |
| contractProofProposalLow | 10 |
| mutationProbes | 50 |
| mutationKilled | 47 |
| mutationSurvived | 0 |
| mutationEquivalent | 3 |
| mutationUnprobedActions | 94 |
| mutationStrictPass | yes |
| mutationProbeCandidates | 94 |
| mutationProbeCandidatesHigh | 46 |
| mutationProbeCandidatesWithoutTests | 24 |
| mutationGuardCandidates | 0 |
| mutationActionsWithGuardCandidates | 0 |
| browserCaseProposals | 28 |
| browserCaseProposalsHigh | 7 |
| routeActionDecisions | 0 |
| contractProofDecisions | 11 |
| proposalDecisionsPending | 0 |
| proposalDecisionsAccepted | 1 |
| proposalDecisionsRejected | 10 |
| proposalReviewReady | yes |
| mutationCoverageReady | no |
| proposalDecisionsValid | yes |

## Readiness gates

| Gate | Ready |
| --- | --- |
| endToEndReady | no |
| routeActionAuditReady | yes |
| obligationAttributionReady | no |
| browserEvidenceReady | no |
| featureMapReady | yes |
| bugDiscoveryReady | yes |
| proposalReviewReady | yes |
| mutationCoverageReady | no |
| proposalDecisionsValid | yes |
| leverReady | yes |

## Missing obligations

| Binding | Count |
| --- | ---: |
| route | 19 |
| candidate | 70 |
| unbound | 75 |

## Next worklist

| Tier | Route | Status | Route action | Next gap |
| ---: | --- | --- | --- | --- |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-routes` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-resolutions` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections/{selectionRevision}/explicit-groups` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/column-source` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/frame-source-options` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/related-field-choices` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-lineage` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 2 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-capabilities` | modeled-only | empty | choose contract-tested with a route test or unproven |
| 1 | `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/builder` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-choice-proposals` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/interpretation-revisions` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/population-mapping` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/row-change` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/selections` | modeled-only | candidate | add trace, schedule, and executable test |
| 2 | `POST /api/v1/projects/{project}/explorers` | modeled-only | candidate | add trace, schedule, and executable test |
| 2 | `DELETE /api/v1/projects/{project}/explorers/{explorerId}` | modeled-only | candidate | add trace, schedule, and executable test |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/publish` | unproven | empty | assign a model action or exclude |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/table-shape-category-discoveries` | unproven | empty | assign a model action or exclude |
| 1 | `POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/construction-category-discoveries` | trace-tested | traced | add browser evidence |

## Blockers

- `get:api-v1-projects-project-explorers-explorerId-authoring-v2-builder` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-category-discoveries` is `trace-tested`. add browser evidence.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-construction-choice-proposals` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-interpretation-revisions` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-population-mapping` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-population-routes` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-publish` is `unproven`. assign a model action or exclude.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-row-change` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-category-discoveries` is `unproven`. assign a model action or exclude.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-resolutions` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-selections` is `modeled-only`. add trace, schedule, and executable test.
- `get:api-v1-projects-project-explorers-explorerId-selections-selectionRevision` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-selections-selectionRevision-explicit-groups` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers` is `modeled-only`. add trace, schedule, and executable test.
- `delete:api-v1-projects-project-explorers-explorerId` is `modeled-only`. add trace, schedule, and executable test.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-column-source` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-frame-source-options` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-related-field-choices` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-row-lineage` is `modeled-only`. choose contract-tested with a route test or unproven.
- `post:api-v1-projects-project-explorers-explorerId-authoring-v2-table-shape-capabilities` is `modeled-only`. choose contract-tested with a route test or unproven.

## Bounds

This report ranks work. It does not prove a route. The strict route check and the full formal runner remain the gates.
