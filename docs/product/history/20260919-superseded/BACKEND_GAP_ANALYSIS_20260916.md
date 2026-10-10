# Backend gaps for researcher-authored ML datasets

## Scope and evidence

Initial investigation, 2026-09-16. This is a capability assessment, not an approved implementation plan. No production code changed.

Source baseline: `arch/integration` at `a921b9e5dca1d42a84a836286140fb3b4d704f3b`, inspected in `/private/tmp/loom-arch-integration`. The planning checkout contains older implementation code and was not the source baseline. GitNexus was refreshed against the integration checkout, then findings were checked in source. Graph coverage is incomplete, so search absence alone is not proof that a capability does not exist.

The product requirement is to let a researcher select a population, choose what each row represents, define meaningful features, inspect exceptions, and export a reproducible dataset. Neither Patient nor DocumentReference must be the mandatory root. Unknown source data must remain accessible; an interpretation library must not become a whitelist that silently removes it.

## Main finding

The lower-level recipe engine is substantially more capable than the incremental authoring API. Required relationships, filters, aggregates, pivots, expansion, and custom identity already exist below that API. The largest new product concepts are a durable starting collection, independently scoped feature definitions, reusable human interpretations, and evidence that explains the resulting values and omissions.

Adding controls to the current Builder alone cannot supply those concepts. Replacing the entire query engine would also discard useful existing machinery.

## Capability and gap register

All source paths below refer to the integration baseline.

| ID | Researcher action | Existing backend | Gap and classification |
| --- | --- | --- | --- |
| BG01 | Start from any suitable resource | Generic resource roots and named grains; eligible catalog nodes can become roots. | Reusable. Do not introduce a Patient-only or DocumentReference-only restriction. |
| BG02 | Use selected files or records as the starting population | Recipe filters and published Viewer filters exist. | New authoring model. The durable V2 document has no independent collection with explicit membership, query-based selection, exclusions, or pinned membership version. |
| BG03 | Change from file rows to specimen rows without losing the starting selection | Root changes are supported. | New semantics. `SET_TABLE_ROOT` replaces the route and clears columns, fixed filters, and actions. It does not preserve selection-to-row mappings or explain deduplication. |
| BG04 | Follow relationships in either direction | Capability evidence emits incoming and outgoing directions. | Reusable navigation, with an authoring restriction to investigate. The same relationship cannot be added twice anywhere in a query, which limits independent feature scopes using that relationship. |
| BG05 | Add a count, existence test, typed lookup, or component value | Typed sources and aggregate compilation already exist. | API gap. `ADD_COLUMN` always creates a field source; `UPDATE_COLUMN` changes label and presentation, not the source definition. A frontend widget alone cannot expose the existing source union. |
| BG06 | Filter the population separately from records contributing to a feature | Lower recipes support root and child filters, plus required and optional traversals. | Authoring/compiler gap. V2 routes compile as optional and have no authored traversal predicates or required-match policy. Viewer fixed filters are not an equivalent substitute. |
| BG07 | Choose how multiple matching records become a value | Lower execution supports first, all, distinct, aggregates, and expansion. | Correctness and authoring gap. Related scalar fields can become `FIRST(FLATTEN(...))`, while public lossless/ML-ready flags inspect nested field repetition rather than relationship multiplicity. Array-shaped distinct aggregates also need contract validation. |
| BG08 | Apply or repair a reusable interpretation of source concepts | Typed FHIR sources, recipe pivots, and versioned expression fragments provide building blocks. | Product workflow not established. A researcher-facing definition needs dataset scope, structural binding, versioning, conflict handling, preview of affected records, and explicit treatment of unmatched records. Existing fragments are expression macros, not proof of this workflow. |
| BG09 | Define latest-before-event or normalized-unit features | Recipe expressions, aggregates, filters, and slices provide parts of the calculation. | Authoring gap, with lower-engine coverage still to prove. No coherent public feature contract specifies time anchor, window, ordering and ties, unit policy, and missing-value meaning. Do not assume the entire calculation engine is absent. |
| BG10 | Ask why a cell is null, ambiguous, or populated | Emitted columns retain source resource/path and compilation identities. | New evidence response. Preview does not return per-cell contributing records or distinguish no match, ambiguous match, invalid value, and absent value. Column-level lineage is insufficient. |
| BG11 | Check the complete dataset before publication | Receipt-backed Preview rejects stale generation and authorization scope; published aggregates/facets exist. | New quality workflow. Preview returns bounded rows and a sample row count, with an empty diagnostics array. It does not establish full-population coverage, ambiguity, duplicate-row identity, or omission counts. |
| BG12 | Download data with its exact meaning and provenance | Publication/materialization identities, streaming backend export code, and CSV/TSV/JSON/JSONL formats exist. The UI guards against materialization changes during paginated export. | Artifact/API gap. A pinned downloadable bundle containing data, feature definitions, selection membership, interpretation versions, quality results, and provenance is not established by the current export path. |
| BG13 | Match a concept by its coding system and code together | A special coding-by-system source correlates items in a Coding array. | Inconsistent support across operations. Typed code filters explicitly reject system/display constraints because paired Coding lowering is unavailable there. Catalog concept-column discovery retains text/display/code strings without system identity. Do not treat a display label or bare code as a universal concept identity. |
| BG14 | Browse meaningful observed concepts instead of raw paths | Profiling retains pivot families, extension observations, and discovery evidence. | API projection gap. V2 catalog conversion exposes generic field candidates but does not carry the richer pivot/extension metadata or blocked-candidate records into a browsable concept model. |

## Source anchors

- BG01: `internal/dataframe/spec/grain.go:45`, `internal/explorer/authoringv2/commands.go:195`.
- BG02: `internal/explorer/authoringv2/types.go:17`, `internal/dataframe/recipe/document_types.go:129`.
- BG03: `internal/explorer/authoringv2/commands.go:273`.
- BG04: `internal/server/explorer_capability.go:260`, `internal/explorer/authoringv2/commands.go:303`.
- BG05: `internal/explorer/authoringv2/semantic_types.go:29`, `internal/explorer/authoringv2/commands.go:411`, `internal/explorer/authoringv2/commands.go:415`.
- BG06: `internal/explorer/compilation/semantic_compile.go:358`, `internal/dataframe/recipe/document_types.go:312`, `internal/server/explorer_receipt_contract.go:222`.
- BG07: `internal/dataframe/compiler/render/aql/navigation_render.go:257`, `internal/explorer/compilation/semantic_compile.go:154`, `internal/explorer/compilation/semantic_compile.go:184`.
- BG08: `internal/explorer/authoringv2/semantic_types.go:29`, `internal/dataframe/recipe/document_types.go:214`, `internal/dataframe/recipe/fragments.go:12`.
- BG09: `internal/explorer/authoringv2/semantic_types.go:29`, `internal/dataframe/recipe/document_types.go:306`.
- BG10: `internal/explorer/compilation.go:5`, `internal/server/explorer_preview_response.go:143`.
- BG11: `internal/explorer/lifecycle/preview.go:12`, `internal/server/explorer_preview_response.go:207`.
- BG12: `internal/api/graphql/graph/dataframe/export.go:15`, `internal/dataframe/published/export.go:5`, `ui/packages/loom-ui/src/api.ts:741`.
- BG13: `internal/dataframe/spec/filter_semantics.go:39`, `internal/catalog/helpers.go:162`, `internal/explorer/compilation/semantic_compile.go:431`.
- BG14: `internal/catalog/types.go:122`, `internal/server/explorer_capability.go:425`.

The semantic-source investigation also found existing correlated extension extraction in `internal/dataframe/recipe/schema/extension_columns.go:13` and Observation code/value pivot metadata in `internal/fhir/schema/pivots.go:176`. These are reuse candidates, not evidence that every custom interpretation already works. In particular, the special authoring lookup uses a fixed list of scalar fallbacks, while the lower extension schema has richer observed value-path/type information.

## Executable checks completed

The following focused tests passed against the integration baseline:

1. `TestApplyCommandsOwnsNestedRouteAndColumnIdentities`
2. `TestApplyCommandsUpdatesRouteEdgeWithoutReplacingOccurrenceState`
3. `TestPreviewRejectsStaleGenerationAndScope`
4. `TestBuildAndRenderGenericPhysicalPlanReducesDirectChildFieldsOnce`
5. `TestBuildAndRenderGenericPhysicalPlanOptionalChildFieldsAndFilters`
6. `TestCapabilityEvidencePublishesBothStoredRelationshipDirections`

Command:

```sh
rtk proxy env GOCACHE=/private/tmp/loom-go-cache go test \
  ./internal/explorer/authoringv2 \
  ./internal/explorer/lifecycle \
  ./internal/dataframe/compiler \
  ./internal/server \
  -run 'TestApplyCommandsOwnsNestedRouteAndColumnIdentities|TestApplyCommandsUpdatesRouteEdgeWithoutReplacingOccurrenceState|TestPreviewRejectsStaleGenerationAndScope|TestBuildAndRenderGenericPhysicalPlanReducesDirectChildFieldsOnce|TestBuildAndRenderGenericPhysicalPlanOptionalChildFieldsAndFilters|TestCapabilityEvidencePublishesBothStoredRelationshipDirections' \
  -count=1
```

These are command, lifecycle, capability, and rendered-plan checks. They are not a live browser walkthrough or proof that arbitrary FHIR pairings remain associated correctly. No performance improvement was implemented or measured. Source-derived correctness concerns need literal-value fixtures before fixes are accepted.

## Next investigation units

Use user-observable scenarios to resolve the remaining backend questions before writing work packages:

1. Select specific files, derive specimen rows, preserve the exact file population, and explain shared or missing specimen links. Repeat with a non-file starting collection.
2. Add two independently scoped features through the same relationship, such as separate counts for two Observation concepts. Demonstrate population filtering separately from contributor filtering.
3. Give one row several related records and nested components. Verify code/value pairing, explicit selection or aggregation, ties, and truthful shape/losslessness metadata with exact expected values.
4. Repair one recurring interpretation, preview affected and unresolved records, preserve raw values, and show how the interpretation version enters receipt identity.
5. Trace one populated cell and each reason for a missing cell to its contributing records, then export the same pinned result with its definitions and quality summary.

Each resulting work package should state the researcher action, existing machinery reused, smallest missing backend contract, and an executable acceptance scenario. Focused backend tests should cover backend-only changes. Use the existing hot-reload Docker verification loop for each completed user journey and the integrated final pass, rather than requiring a full browser run for every internal issue.
