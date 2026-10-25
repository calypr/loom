# Test obligations

Status: generated from the TLA models and existing test metadata.

## Script quality inventory

678 MJS files in the reviewed baseline are accounted for as active or retired records.
678 are currently tracked; 2 baseline files have retained tombstones.
13186 semantic claims are defined once; 9257 source occurrences are direct test assertions, out of 19003 total labeled occurrences.
9746 other source-evidence occurrences are labeled by kind and do not count as direct test assertions.
680 catalog records have source-review evidence; 680 MJS records remain runtime-unverified.
69 files are classified as independent tool/module purposes; 294 MJS files and 5331 semantic claims have no canonical journey mapping.
`role` and `testLayer` are source-reviewed classifications. Configured commands, pinned sourceFiles, and literal imports are separate per-file connections.

| Role | Files |
| --- | ---: |
| browser-spec | 96 |
| browser-workflow | 146 |
| config | 5 |
| helper-or-oracle | 144 |
| test | 220 |
| tool-or-module | 69 |

### Journey assembly by canonical case

| Canonical case | Source files | Direct claims | Partial claims | Supporting claims | Registered check refs | Candidate formal models |
| --- | ---: | ---: | ---: | ---: | ---: | --- |
| `basic-coded-pivot:integer-lifecycle` | 7 | 7 | 30 | 11 | 8 | BuilderPreview |
| `basic-named-cohort-presence:nonempty-zero-match` | 12 | 47 | 26 | 34 | 20 | BuilderOperations, BuilderPreview |
| `basic-nested-coding-expand:nested-coding` | 9 | 36 | 23 | 20 | 11 | BuilderOperations, BuilderPreview |
| `basic-pivot-unpivot:lifecycle` | 23 | 14 | 70 | 16 | 15 | BuilderPreview |
| `basic-related-expand-field-lifecycle:observation-root-medication-administration` | 4 | 1 | 0 | 126 | 20 | BuilderOperations, BuilderPreview |
| `basic-related-field-after-unpivot:gender-all` | 5 | 19 | 0 | 4 | 28 | BuilderPreview |
| `basic-related-field-after-unpivot:gender-missing-all` | 5 | 19 | 0 | 4 | 28 | BuilderPreview |
| `basic-related-quantity-one-all:basic-height-quantity-one-all-lifecycle` | 25 | 5 | 0 | 21 | 6 | BuilderPreview |
| `basic-standalone-coded-fields:coded-fields` | 5 | 6 | 0 | 140 | 7 | BuilderOperations, BuilderPreview |
| `basic-temporal-filter:date` | 3 | 27 | 0 | 4 | 9 | — |
| `basic-temporal-filter:date-time` | 3 | 26 | 0 | 4 | 8 | — |
| `basic-unpivot-group:unpivot-to-group` | 5 | 67 | 0 | 4 | 13 | — |
| `basic-unpivot-pivot-roundtrip:unpivot-to-pivot` | 4 | 4 | 0 | 107 | 16 | BuilderOperations, BuilderPreview |
| `builder-add-column-coverage:basic-field-coverage` | 4 | 4 | 43 | 57 | 10 | BuilderOperations, BuilderPreview |
| `builder-authoring:authoring` | 3 | 0 | 0 | 8 | 24 | — |
| `builder-authoring:cohort-expand` | 15 | 38 | 0 | 9 | 20 | — |
| `builder-authoring:cohort-recode` | 8 | 1 | 0 | 123 | 15 | BuilderOperations, BuilderPreview |
| `builder-authoring:condition-specimen-route-ambiguity` | 6 | 82 | 0 | 4 | 18 | — |
| `builder-authoring:dataframe-contract-panel` | 3 | 0 | 0 | 3 | 17 | — |
| `builder-authoring:explorer-selection` | 16 | 11 | 0 | 4 | 12 | BuilderPreview |
| `builder-authoring:group-entry` | 6 | 9 | 0 | 8 | 20 | — |
| `builder-authoring:group-entry-raw-basic` | 5 | 9 | 0 | 6 | 19 | — |
| `builder-authoring:legacy-v9-rows` | 5 | 0 | 0 | 112 | 24 | BuilderOperations, BuilderPreview |
| `builder-authoring:patient-one-disagreement` | 6 | 0 | 0 | 74 | 8 | BuilderOperations, BuilderPreview |
| `builder-authoring:repeated-empty` | 5 | 12 | 0 | 7 | 54 | — |
| `builder-authoring:selected-resource-coverage` | 7 | 8 | 0 | 88 | 24 | BuilderOperations, BuilderPreview, Population |
| `builder-authoring:suggestions` | 5 | 1 | 0 | 18 | 12 | BuilderOperations, BuilderPreview |
| `builder-authoring:table-reorder` | 5 | 8 | 0 | 5 | 15 | BuilderPreview |
| `builder-coded-source-column:coded-source-column` | 8 | 11 | 33 | 3 | 12 | BuilderPreview |
| `builder-combine-draft:append` | 6 | 12 | 1 | 24 | 21 | BuilderOperations, BuilderPreview |
| `builder-combine-draft:append-derived-edit` | 6 | 18 | 7 | 26 | 44 | BuilderOperations, BuilderPreview |
| `builder-combine-draft:group-pivot` | 6 | 11 | 3 | 23 | 21 | BuilderOperations, BuilderPreview |
| `builder-combine-draft:group-pivot-append` | 6 | 8 | 13 | 24 | 31 | BuilderOperations, BuilderPreview |
| `builder-combine-draft:join` | 6 | 19 | 0 | 24 | 25 | BuilderOperations, BuilderPreview |
| `builder-combine-draft:membership` | 6 | 21 | 0 | 23 | 50 | BuilderOperations, BuilderPreview |
| `builder-combine-nullable:lifecycle` | 10 | 0 | 2 | 26 | 65 | — |
| `builder-combine:append` | 3 | 22 | 0 | 6 | 22 | BuilderOperations, BuilderPreview |
| `builder-combine:join` | 5 | 16 | 0 | 15 | 16 | BuilderOperations, BuilderPreview |
| `builder-controls:explorer-deletion` | 12 | 28 | 0 | 4 | 16 | — |
| `builder-controls:explorer-deletion-unavailable` | 10 | 6 | 0 | 29 | 10 | BuilderOperations, BuilderPreview, Population |
| `builder-controls:first-table` | 6 | 18 | 0 | 5 | 11 | — |
| `builder-controls:recompile` | 5 | 6 | 0 | 3 | 3 | — |
| `builder-controls:tables` | 7 | 6 | 0 | 37 | 11 | BuilderOperations, BuilderPreview |
| `builder-load:list` | 7 | 4 | 0 | 9 | 5 | — |
| `builder-load:state` | 7 | 4 | 0 | 8 | 5 | — |
| `builder-population-member-removal:mapped-contributor-removal-preserves-group-counts` | 5 | 30 | 0 | 8 | 12 | — |
| `builder-population-member-removal:mapped-plus-orphan-to-empty` | 5 | 40 | 0 | 2 | 11 | — |
| `builder-published-membership:membership` | 6 | 24 | 0 | 32 | 115 | BuilderOperations, BuilderPreview |
| `builder-repeated:repeated-expand-pivot` | 4 | 1 | 19 | 6 | 12 | BuilderOperations, BuilderPreview |
| `builder-root-replacement:replace-current-setup` | 20 | 11 | 180 | 27 | 16 | BuilderPreview |
| `builder-saved-route-edit:relatedexpand-exact-one-removal` | 5 | 11 | 0 | 4 | 15 | BuilderPreview |
| `builder-saved-route-edit:saved-related-expand-route-edit` | 5 | 15 | 0 | 4 | 19 | BuilderPreview |
| `cda-authored-expand-filter:expand-filter-upstream-edit-cascade` | 8 | 0 | 0 | 29 | 14 | — |
| `cda-authored-nested-expand:nested-coding-expand-lifecycle` | 24 | 23 | 0 | 67 | 22 | BuilderOperations, BuilderPreview |
| `cda-authored-nested-expand:nested-coding-expand-lifecycle-single-per-component` | 24 | 23 | 0 | 67 | 22 | BuilderOperations, BuilderPreview |
| `cda-builder-native:Preview limits` | 11 | 4 | 1 | 19 | 12 | BuilderOperations, BuilderPreview |
| `cda-cohort-fields:cohort-fields` | 25 | 4 | 2 | 162 | 11 | BuilderOperations, BuilderPreview, Population |
| `cda-collection-repair-partial:partial-long-route-repair-and-reload` | 8 | 0 | 0 | 152 | 13 | BuilderOperations, BuilderPreview |
| `cda-contributor-equals:contributor-equals` | 28 | 14 | 51 | 6 | 15 | BuilderPreview |
| `cda-contributor-exists:contributor-exists` | 31 | 16 | 6 | 95 | 17 | BuilderPreview |
| `cda-current-draft-diagnostic-report-append:diagnostic-report-append` | 27 | 43 | 0 | 14 | 37 | — |
| `cda-current-draft-membership:membership` | 17 | 58 | 48 | 44 | 51 | BuilderOperations, BuilderPreview |
| `cda-current-draft-upstream-append:patient-membership-handoff` | 15 | 9 | 0 | 16 | 13 | BuilderPreview |
| `cda-current-draft-upstream-append:upstream-append` | 25 | 13 | 0 | 6 | 26 | BuilderPreview |
| `cda-expanded-publication:component-row-expand-publish-clickhouse-viewer-reload` | 22 | 1 | 0 | 99 | 2 | BuilderPreview |
| `cda-filter-browser:composite-group-filter` | 15 | 67 | 0 | 18 | 21 | — |
| `cda-filter-browser:filter-lifecycle` | 16 | 48 | 6 | 14 | 10 | — |
| `cda-five-hop-related-expansion:medication-positive-fixture` | 21 | 3 | 6 | 109 | 14 | BuilderPreview |
| `cda-five-hop-related-expansion:medication-preserve-parent` | 21 | 1 | 0 | 5 | 11 | BuilderPreview |
| `cda-group-numeric-filter:numeric-filter-after-group` | 11 | 8 | 0 | 151 | 20 | BuilderOperations, BuilderPreview |
| `cda-named-cohort-related-presence:related-presence-including-empty-group` | 5 | 0 | 0 | 3 | 14 | — |
| `cda-named-cohort-related-presence:related-presence-nonempty-zero-match` | 5 | 0 | 0 | 3 | 14 | — |
| `cda-plain-patient-root-selector:select-observation-directly-and-restore-patient` | 5 | 0 | 115 | 10 | 9 | BuilderPreview |
| `cda-publication:bounded-specimen-publication-viewer-reload` | 7 | 55 | 0 | 1 | 2 | — |
| `cda-published-source-membership:published-membership` | 16 | 0 | 0 | 84 | 59 | BuilderOperations, BuilderPreview |
| `cda-published-upstream-append:published-append` | 27 | 42 | 0 | 10 | 34 | — |
| `cda-related-one-all-observation-status:observation-status-one-to-all-same-chooser-lifecycle` | 25 | 1 | 0 | 13 | 11 | BuilderPreview |
| `cda-related-one-all-specimen-reference:cda-specimen-reference-raw-oracle-one-all-lifecycle` | 23 | 0 | 0 | 8 | 2 | BuilderPreview |
| `cda-related-one-all-zero-observation:zero-patient-observation-exclude-policy-edit-cancel-apply-restoration` | 26 | 10 | 0 | 19 | 11 | BuilderPreview |
| `cda-related-one-all-zero-observation:zero-patient-observation-one-all-stable-parent-lifecycle` | 28 | 12 | 0 | 21 | 13 | BuilderPreview |
| `cda-related-one-all-zero-observation:zero-patient-observation-starting-root-roundtrip` | 26 | 9 | 0 | 28 | 9 | BuilderPreview |
| `cda-repeated-empty:preserve-parent-exclude-error-group-count-and-restoration` | 22 | 0 | 6 | 203 | 5 | BuilderOperations, BuilderPreview, Population |
| `cda-repeated-missing-component-group:missing-component-expanded-group-cancel-restore` | 15 | 1 | 5 | 164 | 3 | BuilderOperations, BuilderPreview, Population |
| `cda-repeated-rows:component-array-source-expand-field-remove-and-row-restoration` | 7 | 40 | 0 | 18 | 16 | — |
| `cda-root-rebase:preserve-patient-values-through-observation-and-restore` | 9 | 99 | 0 | 16 | 9 | — |
| `cda-same-table-root-replacement:same-table-root-replacement` | 16 | 8 | 53 | 16 | 10 | BuilderPreview |
| `cda-semantic-specimen-fields:specimen-semantic-fields` | 10 | 44 | 0 | 4 | 10 | — |
| `cda-semantic-specimen-inventory-fallback:inventory-fallback` | 10 | 39 | 0 | 4 | 9 | — |
| `cda-source-fields:source-fields` | 20 | 8 | 94 | 8 | 9 | BuilderPreview |
| `cda-starting-collection-handoff:initial-selection-handoff-route-preview-apply-reload` | 18 | 50 | 0 | 8 | 10 | — |
| `cda-starting-collection-replacement:cross-resource-route-dependencies` | 8 | 13 | 87 | 51 | 14 | BuilderOperations, BuilderPreview |
| `cda-upstream-related-edit-cascade:upstream-edit-cascade` | 23 | 367 | 0 | 34 | 13 | — |
| `cda-viewer-output-dataset-zip:published-observation-repeated-values-zip` | 11 | 0 | 51 | 44 | 15 | BuilderOperations, BuilderPreview |
| `cda-viewer-output-dataset-zip:published-patient-dataset-zip` | 12 | 0 | 49 | 38 | 33 | BuilderOperations, BuilderPreview |
| `cda-workspace-combine:coded-group-native` | 13 | 40 | 0 | 7 | 12 | — |
| `cda-workspace-combine:compound-coded-group-native` | 34 | 53 | 0 | 15 | 11 | — |
| `cda-workspace-combine:compound-coded-group-single` | 10 | 0 | 0 | 2 | 13 | — |
| `cda-workspace-combine:group-join` | 24 | 11 | 0 | 10 | 17 | BuilderPreview |
| `cda-workspace-combine:group-pivot-join` | 22 | 1 | 1 | 48 | 30 | BuilderPreview |
| `cda-workspace-combine:group-pivot-published` | 19 | 0 | 58 | 10 | 19 | BuilderPreview |
| `cda-workspace-combine:nullable-code-join` | 27 | 15 | 32 | 24 | 38 | BuilderPreview |
| `cda-workspace-combine:saved-cda-related-route-edit-epoch126` | 14 | 16 | 54 | 22 | 19 | BuilderOperations, BuilderPreview |
| `cda-workspace-combine:semantic-group` | 22 | 2 | 6 | 60 | 19 | BuilderPreview |
| `cda-workspace-combine:semantic-group-connected` | 11 | 0 | 0 | 245 | 11 | BuilderOperations, BuilderPreview |
| `cda-workspace-combine:semantic-group-expanded` | 24 | 110 | 0 | 11 | 26 | — |
| `cohort-group-pivot-publication:basic` | 6 | 0 | 2 | 137 | 3 | BuilderOperations, BuilderPreview |
| `dev-journey:verify-j01` | 4 | 35 | 0 | 3 | 35 | — |
| `dev-journey:verify-j05` | 5 | 41 | 0 | 3 | 59 | — |
| `root-quantity-pivot:fixture-lifecycle` | 9 | 58 | 0 | 14 | 13 | — |
| `root-quantity-pivot:full-population-discovery` | 7 | 38 | 0 | 6 | 5 | — |
| `root-quantity-pivot:full-population-lifecycle` | 7 | 97 | 0 | 6 | 10 | — |
| `root-quantity-pivot:related-text-only-full-population-lifecycle` | 26 | 275 | 0 | 77 | 13 | BuilderOperations |
| `scope-001-restricted-auth:restricted-related-records` | 10 | 34 | 0 | 105 | 10 | BuilderOperations, BuilderPreview |
| `standalone-misc:coded-group-recorded-code-basic` | 5 | 26 | 0 | 93 | 26 | BuilderOperations, BuilderPreview |
| `standalone-misc:compound-coded-group-basic` | 10 | 17 | 0 | 1 | 11 | — |
| `standalone-misc:semantic-construction-choice-route-fallback` | 4 | 7 | 0 | 105 | 11 | BuilderOperations, BuilderPreview |
| `standalone-reshape-coded-pivot:coded-pivot-integer` | 12 | 1 | 54 | 60 | 10 | BuilderOperations, BuilderPreview |
| `standalone-reshape-coded-pivot:coded-pivot-string` | 12 | 1 | 54 | 60 | 10 | BuilderOperations, BuilderPreview |
| `standalone-reshape-group-add-fields:group-add-fields` | 30 | 55 | 0 | 27 | 16 | — |
| `standalone-reshape-group-edit-before-related-column:group-edit-before-related-column` | 27 | 17 | 81 | 31 | 17 | BuilderPreview |
| `standalone-reshape-group-one-conflict:group-one-conflict` | 16 | 20 | 0 | 13 | 11 | — |
| `standalone-reshape-related-field-after-unpivot-gender-all:related-field-after-unpivot-gender-all` | 15 | 82 | 0 | 3 | 22 | — |
| `standalone-reshape-related-source-after-pivot:related-resource-type-after-pivot` | 4 | 79 | 0 | 27 | 24 | — |
| `standalone-reshape-related-source-after-pivot:related-resource-type-after-pivot-group-split` | 4 | 89 | 0 | 27 | 35 | — |
| `standalone-reshape-related-source-after-pivot:related-source-after-pivot` | 2 | 77 | 0 | 1 | 23 | — |
| `standalone-reshape-related-source-after-pivot:related-source-count-after-pivot` | 3 | 78 | 0 | 27 | 24 | — |
| `standalone-reshape-related-unpivot:related-unpivot` | 20 | 63 | 3 | 78 | 19 | BuilderOperations |
| `viewer-query:cell-explain` | 10 | 49 | 0 | 65 | 28 | BuilderOperations, BuilderPreview |
| `viewer-query:charts` | 10 | 16 | 34 | 70 | 28 | BuilderOperations, BuilderPreview |
| `viewer-query:configured-output-action-csv` | 9 | 13 | 0 | 64 | 18 | BuilderOperations, BuilderPreview |
| `viewer-query:dataset-zip` | 11 | 16 | 3 | 80 | 34 | BuilderOperations, BuilderPreview |
| `viewer-query:dataset-zip-filtered-complete-population` | 10 | 9 | 17 | 65 | 11 | BuilderOperations, BuilderPreview |
| `viewer-query:output` | 7 | 11 | 2 | 50 | 9 | BuilderOperations, BuilderPreview |
| `viewer-query:pagination` | 11 | 9 | 14 | 67 | 39 | BuilderOperations, BuilderPreview |
| `viewer-query:per-row-file-download` | 10 | 5 | 14 | 68 | 24 | BuilderOperations, BuilderPreview |
| `viewer-query:row-details` | 10 | 2 | 0 | 64 | 19 | BuilderOperations, BuilderPreview |
| `viewer-query:runtime-scope-isolation` | 10 | 21 | 0 | 66 | 27 | BuilderOperations, BuilderPreview |
| `viewer-query:shared-filter-linked-outputs` | 11 | 21 | 3 | 72 | 23 | BuilderOperations, BuilderPreview |

Counts deduplicate semantic claim IDs, source paths, and exact required/focused check references per case. Candidate models come only from files explicitly labeled `formalApplicability.kind: candidate`; these source associations do not establish execution or model refinement.

### Independently scoped tools and unmapped source

| Scope | Count |
| --- | ---: |
| Tool/module files | 69 |
| Tool/module files with a canonical case mapping | 7 |
| Tool/module files without a canonical case mapping | 62 |
| All MJS files without a canonical case mapping | 294 |
| Semantic claims without a canonical case mapping | 5331 |

### Explicit formal links

- `scripts/apply-builder-proposal-decisions.mjs`: candidate; gap: No runtime outcome or route-transition proof is claimed.
- `scripts/generate-builder-route-ledger.mjs`: candidate; gap: No route is asserted proved by this generator; it labels the generated ledger provisional.
- `scripts/loom-dev.mjs`: candidate; gap: No trace correspondence is established for these browser assertions, and the formal model does not cover source-data values, rendered rows, publication, Viewer filters, or downloaded artifacts.
- `scripts/propose-builder-route-actions.mjs`: candidate; gap: Generated actions are explicitly proposals requiring handler review; no action is treated as accepted or proved.
- `scripts/run-builder-bug-discovery.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/run-builder-journey.mjs`: direct-trace → BuilderJourney [Group, GroupAgain, AddColumn, CancelFilter, EditGroup, EditGroupAgain, RemoveGroup, Pivot, Preview, PreviewComplete, PreviewError, PreviewRequestCancelled, LatePreviewCompleteIgnored, LatePreviewErrorIgnored, ChoicePreview, ChoicePreviewComplete, ChoicePreviewError, Stutter]; gap: The runner delegates finite trace checking to verify-builder-traces.mjs; it does not execute the standalone BuilderJourney .cfg safety-invariant suite. Model actions do not cover browser reload, persistence, or concrete row-value assertions.
- `scripts/run-builder-mutation-probe.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/self-test-builder-traces.mjs`: trace-support → BuilderJourney [EditGroup, EditGroupAgain, RemoveGroup, GroupEditCancel, GroupRemovalCancel, Reload, LatePreviewErrorIgnored]; gap: The Group-specific controls require a real captured trace and only run when one is supplied; no fresh native trace or passing Group mutation result is asserted by this source catalogue.
- `scripts/verify-builder-formal.mjs`: direct-trace → BuilderOperations; BuilderPreview; WorkspaceLifecycle; RowDefinitionLifecycle; DraftRestore; RootChange; ColumnPresentation; Population; Interpretation; SupportingSurfaces; LifecycleEntry; ReconcileLifecycle; BuilderJourney; gap: TLC only covers finite states and bounds encoded by the checked-in .cfg files; the source itself states that this is not an unbounded refinement proof of the React or Go application.
- `scripts/verify-builder-interactions.mjs`: direct-trace → ConstructionLifecycle [ChangeDraftIdentity, StartProposal, RetryProposal, CancelProposal, ProposalComplete, ProposalError, LateProposalSettlementIgnored, PreviewComplete, StartPreview, PreviewError, LatePreviewSettlementIgnored, BeginApply]; gap: The model is finite and explicitly stops at beginApply; source model comments exclude persistence/finishApply callback behavior and define the bounded proposal-attempt space.
- `scripts/verify-builder-operation-history.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-builder-traces.mjs`: direct-trace → BuilderPreview [StartProposal, ReplaceProposal, ProposalComplete, ProposalError, LateProposalComplete, CancelProposal, RetryProposal, StartPreview, PreviewComplete, LatePreviewComplete, BeginApply, ApplyAck, ChangeDraft, AcceptCurrentDraft, SetPivotPair, ScheduleDiscovery, DiscoveryComplete, ChangePivotKey, UnmountEditor, AdoptDiscovery]; ConstructionLifecycle [ChangeDraftIdentity, StartProposal, RetryProposal, CancelProposal, ProposalComplete, ProposalError, LateProposalSettlementIgnored, PreviewComplete, StartPreview, PreviewError, LatePreviewSettlementIgnored, BeginApply]; ReconcileLifecycle [RequestReconcile, RetryReconcile, ReconcileSuccess, LateReconcileIgnored, ReconcileStale, ReconcileConflict, ReconcileInvalid, ReconcileFailure, WorkspaceChangeStartsReconcile, DraftChange, AuthoredChange, ChangeExplorer, SetTablesRooted, SetSnapshotPresent, ClearReceipt]; BuilderJourney [Group, GroupAgain, AddColumn, CancelFilter, EditGroup, EditGroupAgain, RemoveGroup, Pivot, Preview, PreviewComplete, PreviewError, PreviewRequestCancelled, LatePreviewCompleteIgnored, LatePreviewErrorIgnored, ChoicePreview, ChoicePreviewComplete, ChoicePreviewError, Stutter, GroupEditCancel, GroupRemovalCancel, Reload]; gap: This is a bounded trace replay, not an exploration of arbitrary browser state. Group row values and UI controls remain native assertions; a native trace is required for the Group controls.
- `scripts/verify-cda-pivot-oracle.mjs`: candidate; gap: The diagnostic relies on an operator-supplied report and hardcoded local CDA runtime identity; it does not run or prove a complete native lifecycle.
- `scripts/verify-construction-removals.mjs`: candidate; gap: The tool does not perform or prove an applied removal lifecycle; no runtime outcome is claimed.
- `scripts/verify-population-row.mjs`: candidate; gap: The retained selection-evidence fixture and command invocation are not statically located, so its intended end-to-end runner remains unresolved.
- `scripts/verify-ui/fast-specs/builder-preview-read-only.spec.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/basic-coded-pivot-fixture.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/basic-named-cohort-presence-oracle.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/basic-nested-coding-expand-oracle.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/basic-pivot-unpivot-oracle.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/basic-published-membership-oracle.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/basic-root-replacement-contract.mjs`: candidate; gap: Candidate correspondence covers only helper-level state ownership; raw oracle values, browser rendering, persistence, and a complete user trace remain outside the formal model.
- `scripts/verify-ui/helpers/builder-combine-helpers.mjs`: candidate; gap: Candidate correspondence covers only helper-level state ownership; raw oracle values, browser rendering, persistence, and a complete user trace remain outside the formal model.
- `scripts/verify-ui/helpers/cda-current-draft-membership-oracle.test.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/cda-starting-collection-replacement-oracle.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/coded-pivot-fixture.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/complete-preview-row-projection.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/composed-row-lineage-fixture.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/construction-capabilities-readiness.mjs`: candidate; gap: Candidate correspondence covers only helper-level state ownership; raw oracle values, browser rendering, persistence, and a complete user trace remain outside the formal model.
- `scripts/verify-ui/helpers/proposal-reopen-binding.mjs`: candidate; gap: Source review only; no runtime, test, browser, or model result is represented as passing.
- `scripts/verify-ui/helpers/related-choice-stage-context.mjs`: candidate; gap: Candidate correspondence covers only helper-level state ownership; raw oracle values, browser rendering, persistence, and a complete user trace remain outside the formal model.
- `scripts/verify-ui/helpers/related-one-all-lifecycle-contract.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/related-policy-construction.mjs`: candidate; gap: Candidate correspondence covers only helper-level state ownership; raw oracle values, browser rendering, persistence, and a complete user trace remain outside the formal model.
- `scripts/verify-ui/helpers/root-rebase-preservation.mjs`: candidate; gap: Source review only; no runtime, test, browser, or model result is represented as passing.
- `scripts/verify-ui/helpers/saved-preview-binding.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/standalone-coded-field-fixture.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/tests/action-scoped-cancellations.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/basic-add-column-coverage-oracle.test.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/tests/basic-coded-pivot-fixture.test.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/tests/basic-nested-coding-expand-oracle.test.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/tests/basic-published-membership-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/basic-related-quantity-one-all-oracle.test.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/tests/basic-runtime-scope-isolation-oracle.test.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/tests/basic-viewer-charts-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/builder-authoring-request-entries.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/builder-combine-draft.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/builder-combine-nullable.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/builder-combine.test.mjs`: candidate → BuilderOperations [AuthorStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/builder-controls-table-preview-readiness.test.mjs`: candidate → BuilderOperations [AuthorStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/builder-patient-one-summary-locator.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/builder-rendered-grid.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-compound-coded-group-oracle.test.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/tests/cda-current-draft-diagnostic-report-append-oracle.test.mjs`: candidate; gap: No model/action binding or formal trace correspondence is established; the current check remains an independent source/oracle/browser contract.
- `scripts/verify-ui/helpers/tests/cda-current-draft-membership-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-nullable-code-join-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-row-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-same-table-root-replacement-readiness.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-saved-related-route-oracle.test.mjs`: candidate → BuilderOperations [AuthorStep, UndoLastStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/cda-upstream-append-reload-readiness.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/cda-viewer-output-dataset-zip-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/complete-preview-row-projection.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/construction-capabilities-readiness.test.mjs`: candidate; gap: The test uses mocked identity reads and emitted request events; no browser lifecycle pass or model binding is claimed.
- `scripts/verify-ui/helpers/tests/construction-output-selection.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/group-pivot-append-contract.test.mjs`: candidate → BuilderOperations [AuthorStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/group-related-multiset.test.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/tests/missing-component-group-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/native-browser-api-scope.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/native-verification-batch.test.mjs`: candidate; gap: There is no current Builder TLA model for test-runner orchestration, scheduler concurrency, cancellation, or cleanup; this remains separate from Builder journey coverage.
- `scripts/verify-ui/helpers/tests/ndjson-resource-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/nested-authored-expand-oracle.test.mjs`: candidate → BuilderOperations [AuthorStep, UndoLastStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/owned-arangosh-command.test.mjs`: candidate → BuilderOperations [AuthorStep, UndoLastStep]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/playwright-authoring-existing-root-change.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/playwright-authoring-suggestions.test.mjs`: candidate → BuilderOperations [AuthorStep, UndoLastStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/post-pivot-related-source-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/related-quantity-pivot-discovery-process.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/related-quantity-pivot-oracle.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/root-rebase-preservation.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- `scripts/verify-ui/helpers/tests/scope-001-repeated-pivot-oracle.test.mjs`: candidate; gap: Static source review only; no runtime, test, browser, or model pass is asserted.
- `scripts/verify-ui/helpers/tests/scope-001-restricted-auth-workflow.test.mjs`: candidate → BuilderOperations [AuthorStep, UndoLastStep]; BuilderPreview [StartProposal, CancelProposal, StartPreview, BeginApply, ApplyAck]; gap: Request/fixture assertions can exceed the model fields and no runtime pass is claimed.
- `scripts/verify-ui/helpers/tests/verify-cda-builder-patient-related.test.mjs`: candidate; gap: Candidate correspondence is limited to proposal/preview state; raw data values, UI rendering, persistence, and the specific test trace are not model-checked.
- 196 additional formal applicability rows are in the JSON report.

These are source associations, not passing runs. Model actions, invariants, schedule IDs, and journey IDs are listed per link in JSON. The report does not promote an obligation to covered from a label; coverage still requires the existing scoped evidence.

### Dispositions and replacement evidence

| Disposition | Files |
| --- | ---: |
| integrate | 2 |
| keep | 677 |
| merge-candidate | 1 |

| MJS file | Lifecycle / disposition | Purpose and decision reason | Replacement paths and evidence | Remaining proof gap |
| --- | --- | --- | --- | --- |
| `scripts/verify-ui/helpers/group-related-multiset.mjs` | retired / integrate | Compares complete row-value multisets so duplicate counts and values must match exactly. The four complete-population guards now live in cda-row-oracle.mjs; the bounded preview API remains unchanged. | scripts/verify-ui/helpers/cda-row-oracle.mjs; scripts/verify-ui/helpers/tests/cda-row-oracle.test.mjs; scripts/verify-ui/workflows/verify-cda-group-edit-before-related-column-browser.mjs; scripts/verify-ui/helpers/cda-row-oracle.mjs [shared complete-population row oracle with duplicate and ALL-item multiplicity checks]; scripts/verify-ui/helpers/tests/cda-row-oracle.test.mjs [focused controls accept legitimate ordering and reject duplicate, missing, extra, and ALL-multiplicity damage]; scripts/verify-ui/workflows/verify-cda-group-edit-before-related-column-browser.mjs [all four actual Group workflow comparison call sites migrated to the shared oracle] | No remaining proof gap recorded. |
| `scripts/verify-ui/helpers/tests/group-related-multiset.test.mjs` | retired / integrate | Exercises the exact related-row multiset helper for order-independent matching and duplicate-sensitive mismatch rejection, including typed ALL values. Its row-order, duplicate-row, and duplicate ALL-item controls now live in cda-row-oracle.test.mjs and are registered in the CDA Group case. | scripts/verify-ui/helpers/cda-row-oracle.mjs; scripts/verify-ui/helpers/tests/cda-row-oracle.test.mjs; scripts/verify-ui/workflows/verify-cda-group-edit-before-related-column-browser.mjs; scripts/verify-ui/helpers/cda-row-oracle.mjs [shared complete-population row oracle with duplicate and ALL-item multiplicity checks]; scripts/verify-ui/helpers/tests/cda-row-oracle.test.mjs [focused controls accept legitimate ordering and reject duplicate, missing, extra, and ALL-multiplicity damage]; scripts/verify-ui/workflows/verify-cda-group-edit-before-related-column-browser.mjs [all four actual Group workflow comparison call sites migrated to the shared oracle] | No remaining proof gap recorded. |
| `ui/packages/loom-ui/scripts/verify-cda-contributor-exists-browser.mjs` | active / merge-candidate | Drives a standalone native CDA Contributor EXISTS flow and compares UI previews, saved policy changes, and source restoration against project/generation-scoped raw Arango witnesses. Keep as a merge candidate for the registered `cda-contributor-exists:contributor-exists` route: both workflows cover zero/one/many Contributor EXISTS policies, but the candidate uses an unbounded many-witness query while the canonical oracle is limited to the first 2,000 Patients and 2–22 many rows. The candidate only checks proposal DOM rows; the canonical path checks native request/response CAS, receipt and candidate binding, plus complete non-sampled preview evidence. Retain until parity is proved. | scripts/verify-ui/specs/standalone-cda-fields.spec.mjs; scripts/verify-ui/workflows/contributor-exists-workflow.mjs; scripts/verify-ui/registry.mjs; scripts/verify-ui/registry.mjs [registered-canonical-case-checks-and-focus-binding]; scripts/verify-ui/workflows/contributor-exists-workflow.mjs [bounded-canonical-oracle-native-request-response-and-persistence-assertions]; scripts/verify-ui/specs/standalone-cda-fields.spec.mjs [registered-spec-dispatches-canonical-workflow] | Before any consolidation, prove that the legacy zero/one/many witness IDs and every related Observation row fit the canonical first-2,000-Patient, 2-to-22-many contract. Preserve exact ALL_MATCHES and EXISTS ERROR/PRESERVE_PARENT/EXCLUDE results, diagnostics, policy edits, source identities, Cancel/Apply, and reload/removal restoration. The legacy workflow only checks proposal DOM rows; establish equivalent native request/response CAS, receipt, candidate-construction equivalence, complete and non-sampled preview checks, and an error/performance census in the registered route. Also decide whether the generic-Playwright-only `standalone-misc-cda-unmapped` invocation must remain; it has no registry `requiredChecks` contract. |

Disposition labels and replacement evidence come from the source catalog. They do not turn a candidate into runtime proof; independent oracles and replacement gates remain explicit in purpose, evidence, and any nonempty gap.

The source catalog is hash-bound to reviewed revisions; currentSha256 records each active file at generation time. Configured focused commands and sourceFiles are separate registry connections; sourceFiles and literal import candidates are not execution evidence. Only an explicit per-case summary with a passed focused group, exact planned command stage, unchanged complete inputs, and matching current input hashes records focused-command-passed for the command test file. This never upgrades a case, journey, or formal-model result. Semantic claims and formal bindings remain intended associations; retired rows preserve their source and replacement references.

## Summary

| Measure | Count |
| --- | ---: |
| models | 14 |
| actions | 177 |
| invariants | 96 |
| obligations | 273 |
| covered | 10 |
| partial | 96 |
| missing | 167 |
| missingRouteBound | 19 |
| missingCandidate | 70 |
| missingUnbound | 78 |
| tests | 908 |
| testsWithObligations | 11 |
| testsWithoutObligations | 897 |
| obligationsWithMultipleTests | 24 |
| duplicateTestNames | 0 |
| repeatedPlaywrightChecks | 246 |
| sharedProofs | 3 |
| candidateRouteBindings | 166 |

## Obligations with no exact test

- `BuilderJourney.AddColumn` (0 route(s))
- `BuilderJourney.CancelFilter` (0 route(s))
- `BuilderJourney.ChoicePreview` (0 route(s))
- `BuilderJourney.ChoicePreviewComplete` (0 route(s))
- `BuilderJourney.ChoicePreviewError` (0 route(s))
- `BuilderJourney.EditGroup` (0 route(s))
- `BuilderJourney.EditGroupAgain` (0 route(s))
- `BuilderJourney.Group` (0 route(s))
- `BuilderJourney.GroupAgain` (0 route(s))
- `BuilderJourney.GroupEditCancel` (0 route(s))
- `BuilderJourney.GroupRemovalCancel` (0 route(s))
- `BuilderJourney.LatePreviewCompleteIgnored` (0 route(s))
- `BuilderJourney.LatePreviewErrorIgnored` (0 route(s))
- `BuilderJourney.Pivot` (0 route(s))
- `BuilderJourney.Preview` (0 route(s))
- `BuilderJourney.PreviewComplete` (6 route(s))
- `BuilderJourney.PreviewError` (0 route(s))
- `BuilderJourney.PreviewRequestCancelled` (0 route(s))
- `BuilderJourney.Reload` (0 route(s))
- `BuilderJourney.RemoveGroup` (0 route(s))
- `BuilderJourney.Stutter` (0 route(s))
- `BuilderOperations.AuthorStep` (16 route(s))
- `BuilderOperations.ResetWorkspace` (0 route(s))
- `BuilderOperations.RouteSavedStep` (1 route(s))
- `BuilderOperations.UndoLastStep` (0 route(s))
- `BuilderPreview.AcceptCurrentDraft` (0 route(s))
- `BuilderPreview.AdoptDiscovery` (0 route(s))
- `BuilderPreview.CancelProposal` (4 route(s))
- `BuilderPreview.ChangeDraft` (0 route(s))
- `BuilderPreview.ChangePivotKey` (0 route(s))
- `BuilderPreview.LatePreviewComplete` (0 route(s))
- `BuilderPreview.LateProposalComplete` (0 route(s))
- `BuilderPreview.ProposalComplete` (6 route(s))
- `BuilderPreview.ProposalError` (2 route(s))
- `BuilderPreview.ReplaceProposal` (2 route(s))
- `BuilderPreview.RetryProposal` (2 route(s))
- `BuilderPreview.SetPivotPair` (0 route(s))
- `BuilderPreview.StartProposal` (6 route(s))
- `BuilderPreview.UnmountEditor` (0 route(s))
- `ColumnPresentation.CreateRelatedAggregate` (0 route(s))
- `ColumnPresentation.InspectColumn` (1 route(s))
- `ColumnPresentation.InspectRejected` (1 route(s))
- `ColumnPresentation.InspectSucceeded` (1 route(s))
- `ColumnPresentation.MoveColumn` (0 route(s))
- `ColumnPresentation.RemoveConfiguredColumn` (0 route(s))
- `ColumnPresentation.RenameColumn` (0 route(s))
- `ColumnPresentation.SearchColumns` (0 route(s))
- `ColumnPresentation.SetFeaturePolicy` (0 route(s))
- `ColumnPresentation.SetFilterOffered` (0 route(s))
- `ColumnPresentation.SetRowValuePolicy` (0 route(s))
- `ColumnPresentation.ToggleVisibility` (0 route(s))
- `ConstructionLifecycle.BeginApply` (6 route(s))
- `ConstructionLifecycle.CancelProposal` (4 route(s))
- `ConstructionLifecycle.ChangeDraftIdentity` (0 route(s))
- `ConstructionLifecycle.LatePreviewSettlementIgnored` (0 route(s))
- `ConstructionLifecycle.LateProposalSettlementIgnored` (0 route(s))
- `ConstructionLifecycle.PreviewComplete` (6 route(s))
- `ConstructionLifecycle.PreviewError` (0 route(s))
- `ConstructionLifecycle.ProposalComplete` (6 route(s))
- `ConstructionLifecycle.ProposalError` (2 route(s))
- `ConstructionLifecycle.RetryProposal` (2 route(s))
- `ConstructionLifecycle.StartPreview` (6 route(s))
- `ConstructionLifecycle.StartProposal` (6 route(s))
- `DraftRestore.CancelRestore` (0 route(s))
- `DraftRestore.RestoreComplete` (0 route(s))
- `DraftRestore.SaveDraft` (0 route(s))
- `DraftRestore.StaleDraft` (0 route(s))
- `DraftRestore.StartRestore` (0 route(s))
- `Interpretation.ApplyCandidate` (1 route(s))
- `Interpretation.BeginCreate` (1 route(s))
- `Interpretation.CancelCreate` (1 route(s))
- `Interpretation.CreateBlocked` (1 route(s))
- `Interpretation.CreateFailed` (1 route(s))
- `Interpretation.CreateRevision` (1 route(s))
- `Interpretation.ReviewCandidate` (1 route(s))
- `Interpretation.ReviewReady` (1 route(s))
- `Interpretation.ReviewStale` (1 route(s))
- `LifecycleEntry.AuthoredChange` (0 route(s))
- `LifecycleEntry.CommandStale` (0 route(s))
- `LifecycleEntry.CommandSuccess` (0 route(s))
- 87 more in the JSON report.

## Obligations with exact tests

- `BuilderPreview.ApplyAck`: go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, vitest `replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B`
- `BuilderPreview.BeginApply`: go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestBuilderFullUIEmptyRootCreationDispatchUsesRealService`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, vitest `replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B`
- `BuilderPreview.DiscoveryComplete`: go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`
- `BuilderPreview.PreviewComplete`: go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestNativeV2RouteUsesAuthorizedPersistedReceipt`, go `TestOpenAPIDocumentsOperationSpecificFailureStatuses`, vitest `replays fallback preview schedule: ${schedule}`
- `BuilderPreview.ScheduleDiscovery`: go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`
- `BuilderPreview.StartPreview`: go `TestAssessAndApplyRowChangeThroughPublicAPI`, go `TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`, go `TestNativeV2RouteUsesAuthorizedPersistedReceipt`, go `TestOpenAPIDocumentsOperationSpecificFailureStatuses`, vitest `replays fallback preview schedule: ${schedule}`
- `ReconcileLifecycle.LateReconcileIgnored`: go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, go `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer`, vitest `formal mounted reconcile race keeps B visible after late A settles`
- `ReconcileLifecycle.ReconcileSuccess`: go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, go `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer`, vitest `formal mounted reconcile race keeps B visible after late A settles`
- `ReconcileLifecycle.RequestReconcile`: go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, go `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer`, vitest `formal mounted reconcile race keeps B visible after late A settles`
- `ReconcileLifecycle.WorkspaceChangeStartsReconcile`: go `TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt`, go `TestGeneratedExplorerMutationRoutesForwardAuthResourcePath`, go `TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer`, vitest `formal mounted reconcile race keeps B visible after late A settles`

## Obligations with multiple tests

- `BuilderPreview.ApplyAck`: 5 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath)
- `BuilderPreview.BeginApply`: 5 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath)
- `BuilderPreview.PreviewComplete`: 4 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.StartPreview`: 4 tests (server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyAckTargetsInitiatingOwner`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyFailureTargetsInitiatingOwner`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_ApplyUsesReviewedPreview`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_DiscoveryOwnedByCurrentKey`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_DraftMonotonic`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `BuilderPreview.Inv_PreviewOwnedByCurrentProposal`: 9 tests (unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays fallback preview schedule: ${schedule}, unit:vitest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx::replays ready replacement: ready A; start B; reject beginApply(A); ready B; apply only B, server:go:internal/server/explorer_row_change_route_test.go::TestAssessAndApplyRowChangeThroughPublicAPI, server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/explorer/lifecycle/builder_full_ui_replay_test.go::TestBuilderFullUIEmptyRootCreationDispatchUsesRealService, server:go:internal/server/openapi_explorer_construction_proposal_test.go::TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_receipt_contract_test.go::TestNativeV2RouteUsesAuthorizedPersistedReceipt, server:go:internal/server/openapi_route_inventory_test.go::TestOpenAPIDocumentsOperationSpecificFailureStatuses)
- `ReconcileLifecycle.LateReconcileIgnored`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.ReconcileSuccess`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.RequestReconcile`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.WorkspaceChangeStartsReconcile`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_ActiveRequestMatchesCurrent`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_DraftChangeInvalidatesReceipt`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_FailureHasNoReceipt`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_LateResponseDoesNotReplaceCurrentReceipt`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_ReceiptOnlyWhenSucceeded`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_ReconcileRequiresRootedTables`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_RequestMatchesCurrent`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_RetryBounded`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_SuccessReceiptMatchesCurrent`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)
- `ReconcileLifecycle.Inv_WorkspaceChangeStartsReconcileEmpty`: 3 tests (server:go:internal/server/explorer_receipt_contract_test.go::TestBuilderCommandsCreateBackendOwnedDraftAndReconcileIt, server:go:internal/server/openapi_explorer_write_scope_test.go::TestGeneratedExplorerMutationRoutesForwardAuthResourcePath, server:go:internal/server/explorer_reconcile_scope_test.go::TestReconcileSelectedOutputScopesReceiptAndPreviewPastStaleConsumer)

## Duplicate test names

No duplicate test names found.

## Repeated Playwright checks

- `Preview renders both independent fixture Patients`: 3 cases (playwright:builder-controls:recompile, playwright:builder-controls:first-table, playwright:builder-authoring:authoring)
- `open native Explorer creation action-to-render within budget`: 2 cases (playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable)
- `create the disposable Explorer through the native control action-to-render within budget`: 2 cases (playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable)
- `fixture browser observer reported no unexpected network or runtime errors`: 8 cases (playwright:builder-controls:explorer-deletion, playwright:builder-controls:explorer-deletion-unavailable, playwright:builder-authoring:authoring, playwright:basic-unpivot-group:unpivot-to-group, playwright:viewer-query:dataset-zip, playwright:viewer-query:runtime-scope-isolation, playwright:basic-related-field-after-unpivot:gender-all, playwright:basic-related-field-after-unpivot:gender-missing-all)
- `builder failure is exposed in an alert`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder failure exposes an actionable in-app retry`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder recovered through in-app Retry`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `builder in-app Retry action-to-render within budget`: 2 cases (playwright:builder-load:list, playwright:builder-load:state)
- `created a fresh Explorer distinct from the bootstrap`: 4 cases (playwright:builder-authoring:suggestions, playwright:builder-published-membership:membership, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-nullable:lifecycle)
- `basic Patient fixture contains the two independently known records`: 3 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic, playwright:builder-authoring:legacy-v9-rows)
- `Direct Group action is actionable`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `direct standard Group entry defaults to empty-key COUNT_ROWS without an edit`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `native Builder automatically proposes the exact empty-key COUNT_ROWS GROUP`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `automatic Group proposal Preview returns one row with the independently expected count 2`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `applied empty-key Group renders the independently expected count 2`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `saved Group edit previews exact Patient IDs as one row per key`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `Cancel preserves the saved empty-key Group and exact draft`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `saved Group edit by id and gender preview matches exact fixture IDs, gender values, and row counts`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `applied Group edit by id and gender has the exact two-row, three-column result`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `saved Group edit applies one row per exact Patient ID`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `keyed Group edit and exact Patient rows survive reload`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `reloaded Group editor mounts the exact saved ID-only key and COUNT_ROWS aggregate`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `Exiting the reloaded Group editor without Apply preserves the exact saved workspace and draft`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `Group removal previews both literal source Patient rows`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `Cancel preserves the saved keyed Group and exact draft`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `removing keyed Group restores the exact source Patient rows after reload`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `direct Group entry automatically previews empty-key COUNT_ROWS action-to-render within budget`: 2 cases (playwright:builder-authoring:group-entry, playwright:builder-authoring:group-entry-raw-basic)
- `independent fixture contains one Patient, four Observations, and three DiagnosticReports`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `draft Combine uses the exact project, generation, Builder snapshot, and authorization scope`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `Observation root Preview contains exact independent fixture IDs`: 6 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `first native source table establishes a versioned current draft under the exact scope`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `DiagnosticReport root Preview contains exact independent fixture IDs`: 6 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `DiagnosticReport native source table establishes a versioned current draft under the exact scope`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `native Combine uses CREATE_TABLE to make an empty rooted target without authoring a source`: 3 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:membership)
- `removing Combine and reloading restores its rooted empty target`: 7 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:join, playwright:builder-combine-draft:append, playwright:builder-combine-draft:append-derived-edit, playwright:builder-combine-draft:group-pivot, playwright:builder-combine-draft:group-pivot-append, playwright:builder-combine-draft:membership)
- `open Explorer creation action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-nullable:lifecycle)
- `create blank Explorer action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-nullable:lifecycle)
- `create current-draft Observation source table action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `create current-draft DiagnosticReport source table action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)
- `open native Combine and create a separate empty target action-to-render within budget`: 2 cases (playwright:builder-published-membership:membership, playwright:builder-combine-draft:membership)

## Tests with no exact obligation

- `playwright` `Duplicate, rename, select, delete, copy, and reload tables$` in `scripts/verify-ui/specs/builder-controls.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, WorkspaceLifecycle
- `playwright` `recompile` in `scripts/verify-ui/specs/builder-controls.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Add columns waits for a current-draft preview` in `scripts/verify-ui/specs/builder-controls.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, WorkspaceLifecycle
- `playwright` `Explorer deletion removes the disposable Explorer, preserves the exact fallback URL, and blocks invalid deletes$` in `scripts/verify-ui/specs/builder-explorer-deletion.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, WorkspaceLifecycle
- `playwright` `Explorer deletion stays hidden and preserves the exact Explorer list and URL across reload$` in `scripts/verify-ui/specs/builder-explorer-deletion-unavailable.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, WorkspaceLifecycle
- `playwright` `Builder load list recovery shows the list failure and recovers through the in-app Retry` in `scripts/verify-ui/specs/builder-load.spec.mjs`; suspected models: WorkspaceLifecycle
- `playwright` `Builder load state recovery shows the state failure and recovers through the in-app Retry` in `scripts/verify-ui/specs/builder-load.spec.mjs`; suspected models: WorkspaceLifecycle
- `playwright` `catalog-backed suggestions expose actionable Patient candidates$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Builder authoring previews, changes an existing table root, and reloads$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native Record type picker resolves Condition-to-Specimen alternatives through the Patient-subject path$` in `scripts/verify-ui/specs/condition-specimen-route-ambiguity.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native table reorder preserves exact saved order and rows after reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `cohort-recode` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `cohort-expand` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `repeated-empty` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `standard Group entry proposes an empty-key COUNT_ROWS preview$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Group edit and removal preserve the raw five-column Basic Patient table$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `selected Patient coverage preserves exact membership through detach, reattach, and reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Explorer switching shows distinct tables and restores the selected Explorer after reload$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Dataframe contract panel shows the exact Patient output and lineage$` in `scripts/verify-ui/specs/builder-authoring.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `legacy semantics v9 preserves the saved Patient digest through native row actions and reload$` in `scripts/verify-ui/specs/legacy-v9-workspace.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `raw Patient.id ONE refusal leaves the saved Group intact for same-editor ALL repair$` in `scripts/verify-ui/specs/builder-patient-one-disagreement.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `published MEMBERSHIP uses exact Basic source revisions through INCLUDE, EXCLUDE edit, removal, and reload$` in `scripts/verify-ui/specs/published-membership.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, WorkspaceLifecycle
- `playwright` `native cohort Group→Pivot publishes and reloads exact Viewer counts$` in `scripts/verify-ui/specs/cohort-group-pivot-publication.spec.mjs`
- `playwright` `native coded source column survives edit, reload, removal, and reload$` in `scripts/verify-ui/specs/builder-coded-source-column.spec.mjs`; suspected models: BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, SupportingSurfaces, WorkspaceLifecycle
- `playwright` `Basic fixture expands nested coding with owner, multiplicity, Cancel, Apply, edit, removal and reload$` in `scripts/verify-ui/specs/basic-nested-coding-expand.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture preserves related source and active-record fields through Group lifecycle$` in `scripts/verify-ui/specs/related-expand-field-lifecycle.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic native PRESENCE maps a nonempty zero-match Group to false with exact true comparator and full lifecycle$` in `scripts/verify-ui/specs/basic-named-cohort-presence.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `native component expansion and distinct Pivot survive preview, edit, removal, and reload$` in `scripts/verify-ui/specs/scope-001-repeated-expand-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture adds standalone coded fields with exact Coding version and display correlation$` in `scripts/verify-ui/specs/standalone-coded-fields.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `selects, previews, cancels, applies, edits, removes, and reloads exact coded rows$` in `scripts/verify-ui/specs/basic-coded-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, SupportingSurfaces, WorkspaceLifecycle
- `playwright` `Basic fixture preserves exact row identities through native Pivot→Unpivot lifecycle$` in `scripts/verify-ui/specs/basic-pivot-unpivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture preserves exact Patient rows through native Unpivot→Pivot, edit, reload, and restoration$` in `scripts/verify-ui/specs/basic-unpivot-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture groups exact native Unpivot keys and counts$` in `scripts/verify-ui/specs/basic-unpivot-group.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `date filter keeps exact typed values and source rows through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/basic-temporal-filter.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `date-time filter keeps exact typed values and source rows through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/basic-temporal-filter.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `mapped-plus-orphan-to-empty` in `scripts/verify-ui/specs/population-member-removal.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `removes one mapped contributor, preserves the lower nonempty GROUP, and restores the exact baseline$` in `scripts/verify-ui/specs/population-member-removal.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `full-population-discovery` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `fixture-lifecycle` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `matches SUM and MAX output and restores the full CDA source after reload$` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `related quantity Pivot groups by text and applies SUM/MAX across the full route$` in `scripts/verify-ui/specs/root-quantity-pivot.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `join` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append-derived-edit` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `group-pivot` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `group-pivot-append` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `membership` in `scripts/verify-ui/specs/draft-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `join` in `scripts/verify-ui/specs/join.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `append` in `scripts/verify-ui/specs/append.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `lifecycle` in `scripts/verify-ui/specs/nullable-combine.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, ReconcileLifecycle, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `@dev-journey:verify-j01$` in `scripts/verify-ui/specs/dev-journeys.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `@dev-journey:verify-j05$` in `scripts/verify-ui/specs/dev-journeys.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `output` in `scripts/verify-ui/specs/viewer-query.spec.mjs`
- `playwright` `Viewer shared filters apply to linked outputs, cancel cleanly, and reset on reload$` in `scripts/verify-ui/specs/viewer-shared-filter.spec.mjs`
- `playwright` `Viewer paginates exact fixture Patient identities and resets page state$` in `scripts/verify-ui/specs/viewer-pagination.spec.mjs`
- `playwright` `Viewer row details show the exact Patient ID through close, reopen, reload, and reopen$` in `scripts/verify-ui/specs/viewer-row-details.spec.mjs`
- `playwright` `Viewer explains the exact Patient Gender cell through source details, close, reopen, and reload$` in `scripts/verify-ui/specs/viewer-cell-explain.spec.mjs`
- `playwright` `Viewer charts expose the exact Gender category count and missing count before and after reload$` in `scripts/verify-ui/specs/viewer-charts.spec.mjs`
- `playwright` `configured-output-action-csv` in `scripts/verify-ui/specs/viewer-output-action.spec.mjs`
- `playwright` `Viewer downloads the exact source DocumentReference from a per-row FileCell and repeats after reload$` in `scripts/verify-ui/specs/viewer-row-file-download.spec.mjs`
- `playwright` `Viewer downloads the published Patient dataset as an exact identity-bound ZIP$` in `scripts/verify-ui/specs/viewer-output-dataset-zip.spec.mjs`
- `playwright` `Viewer dataset ZIP exports the complete authorized population while a facet is selected$` in `scripts/verify-ui/specs/viewer-output-filtered-dataset-zip.spec.mjs`
- `playwright` `Basic Viewer output excludes rows from another project and generation$` in `scripts/verify-ui/specs/basic-runtime-scope-isolation.spec.mjs`
- `playwright` `compound-coded-group-basic` in `scripts/verify-ui/specs/standalone-misc.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Basic fixture groups literal Observation codes with exact counts through Preview, Cancel, Apply, edit, removal and reload$` in `scripts/verify-ui/specs/basic-coded-group-recorded-code.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `previews, applies, reloads, and removes the exact optional routed semantic value$` in `scripts/verify-ui/specs/semantic-construction-choice-route.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Preview limits$` in `scripts/verify-ui/specs/standalone-builder.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `create, edit, cancel, apply, and remove a typed CDA filter$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `filter two-key Observation Group output by subject reference through Cancel, Apply, edit, removal, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `Explicit row-root replacement previews a reset, cancels safely, applies, and survives reload$` in `scripts/verify-ui/specs/basic-root-replacement.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `select Observation directly from a plain Patient table and restore Patient$` in `scripts/verify-ui/specs/standalone-cda-plain-patient-root-selector.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `rebase Patient rows through Observation and restore the original route and values$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, RootChange, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `remove an unmapped selected resource, verify the saved route, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `CDA starting collection handoff hand off a starting collection, attach its route, preview, and reload$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, WorkspaceLifecycle
- `playwright` `restricted Calypr auth keeps related Builder rows within the granted path across Cancel and Apply$` in `scripts/verify-ui/specs/scope-001-restricted-auth.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, WorkspaceLifecycle
- `playwright` `replace a dependent starting collection across Patient and Observation, then restore its route lifecycle$` in `scripts/verify-ui/specs/standalone-cda-other.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `replace the Patient setup with the exact default Observation page on the same table$` in `scripts/verify-ui/specs/cda-same-table-root-replacement.spec.mjs`; suspected models: BuilderJourney, BuilderPreview, ConstructionLifecycle, LifecycleEntry, Population, RootChange, WorkspaceLifecycle
- `playwright` `preserves missing-component owners through EXPANDED and GROUP removal Cancel$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `applies source expansion, verifies item values, and restores original Observation rows$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- `playwright` `CDA repeated component empty-list policies preserves empty and populated component owners through row policy and GROUP lifecycle$` in `scripts/verify-ui/specs/standalone-cda-rows.spec.mjs`; suspected models: BuilderJourney, BuilderOperations, BuilderPreview, ConstructionLifecycle, Population, RowDefinitionLifecycle, WorkspaceLifecycle
- 817 more in the JSON report.

## Shared route proof evidence

- `uiTest:ui/packages/loom-ui/src/features/ExplorerBuilder/constructionWorkspace/useConstructionLifecycle.formal.test.tsx`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview
- `goTest:TestConstructionProposalHTTPContractPreviewsAndAppliesRemovalOnly`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview
- `browserCase:builder-controls:recompile`: post:api-v1-projects-project-explorers-explorerId-authoring-v2-commands, post:api-v1-projects-project-explorers-explorerId-authoring-v2-preview, post:api-v1-projects-project-explorers-explorerId-authoring-v2-reconcile

## Bounds

The test index is heuristic. Registry endpoints, Go route strings, and route proofs are strong links. Vitest test names are keyword links. The report marks the mapping on every test.
