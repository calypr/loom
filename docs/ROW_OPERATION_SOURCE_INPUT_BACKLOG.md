# Row operations on unselected dataset inputs

Operator-approved product work, 2026-11-01. Scheduling lives in BUILDER_WORKLIST.tsv. These are feature tasks and source-backed limitations, not a declaration that every item is an existing contract violation.

## Outcome and boundaries

Researchers can use eligible dataset fields and coded concepts in row operators without first adding them as output columns. Group already supports this in some flows; reuse its signed source-choice design. Internal IDs are not the defect. Preserve authorization, generation/snapshot freshness, row identity, multiplicity, missing values, and saved construction semantics. Do not replace signed choices with arbitrary client paths or advertise schema-only fields as observed data.

## Approved sequence

- ROW-SOURCE-01, priority 0, ready. Inspect the non-Group operators and record a compact operator/input matrix covering selected-column prerequisites, existing source choices, and genuine stage constraints. Bound this investigation to selecting the first feasible useful operator. Implement one complete vertical slice before expanding the inventory. Prefer extending existing Pivot source input when the actual source demonstrates a remaining gap; do not rebuild already working behavior. Deliver at least one non-Group operator consuming an eligible unselected field or concept. If that slice already works, prove it briefly and select the next real gap.
- ROW-SOURCE-02, priority 1, blocked on 01. Continue through the remaining eligible non-Group operators from that matrix. Each operator is a coherent implementation and review unit. Ordinary field/scalar, Coding, and CodeableConcept inputs must be evaluated against each operation's real semantics. Do not force source inputs through row-destroying stages. Preserve supported selected-column behavior.
- ROW-SOURCE-03, priority 1, ready for independent investigation, implementation depends on 01's capability contract. Align operation availability with eligible source inputs. Show meaningful field/concept labels and precise reasons for unsupported version, type, route, or stage. Distinguish empty data, loading, inventory errors, and unsupported operations. A picker-only change cannot close an implementation task.
- ROW-SOURCE-04, design-needed, not authorized for implementation yet. Identify the first useful related-route or post-transform coded-operation extension and propose its row/multiplicity/null/identity contract. Source anchors include lifecycle/construction_coded_pivot.go direct-root/direct-source restrictions and construction_semantic_group.go. Independent branch combinations and versioned coded categories require explicit product decisions. Produce a bounded recommendation, not a generic redesign.

## Acceptance for each implemented operator

Use a small independent fixture with only identity initially selected and eligible unselected scalar, Coding, and CodeableConcept inputs. Include missing values and repeated contributors where relevant. Derive expected output rows from the fixture, independently of the compiler under test. Verify discovery, configuration, exact Preview rows, Cancel without mutation, Apply, reload, edit, removal, and source restoration through the real UI/API. Confirm selected-column behavior still works and forbidden/stale choices are refused. Use existing verifier machinery and register a focused journey when missing. UI labels alone, mocked server responses, test counts, or an API-only pass do not establish completion. Record exact source identity, evidence, review, and local commit IDs.

## Source starting points

ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx
ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/ConstructionReshapeEditor.tsx
ui/packages/loom-ui/src/features/ExplorerBuilder/constructionOperations/CodedPivotEditor.tsx
internal/explorer/lifecycle/construction_group_field_choices.go
internal/explorer/lifecycle/construction_pivot_source.go
internal/explorer/lifecycle/construction_coded_pivot.go
internal/explorer/lifecycle/construction_semantic_group.go

## Authority

The operator approved items 01-03 within these bounds. Item 04 authorizes investigation and a proposal only. New feature ideas are proposals until operator-approved; necessary implementation subtasks within approved scope do not need repeated approval. Existing urgent defect work remains recorded and preserved.

## ROW-SOURCE-05: preserve Pivot source removal during Filter preparation

Priority 0 regression repair, operator-approved. Pick up at a safe queue boundary; do not interrupt or duplicate active writers. This is a follow-up to ROW-SOURCE-01, not additional feature scope.

Confirmed against committed snapshot c53759fe. In ProposeConstruction, constructionCandidateWithPivotSources runs before constructionCandidateWithFilterSources. Pivot preparation intentionally skips preservation for the edited Pivot when PivotSources is supplied. The new Filter preparation unconditionally calls preserveConstructionOwnedInputs(base.construction, candidate, "") at filter_source.go:183, then returns for non-Filter edits. This restores the old Pivot-owned projections or related-field helpers that Pivot preparation deliberately cleared. A restored projection no longer consumed by its owner is rejected by authoringv2 validation.

Reproducer: base construction contains a Pivot-owned private source projection; edited candidate Pivot consumes public category/value columns and submits PivotSources: []. Run Pivot preparation and assert SourceProjections is empty. Run Filter preparation on that result and assert it remains empty. On c53759fe, the second assertion fails because old-private-input is restored. Parent review ran the actual functions in an isolated git archive, not the live checkout.

Evidence on Stravinsky: /tmp/opencode/filter-review-c53759fe.log records the failing TestReviewPivotClearSurvivesFilterPreparation. The isolated archive is /tmp/opencode/filter-review-mexsgwwz. Removing only the redundant preservation call in that archive made the reproducer and focused TestConstructionFilterSource tests pass; /tmp/opencode/filter-review-c53759fe-fixed-probe.log records the passing run. These temporary paths are supporting evidence, not durable regression coverage. The live source was not edited by the reviewer.

Acceptance: add a durable cross-operator regression through the complete preparation sequence; repair the first incorrect preservation decision; verify Pivot edits that clear or replace owned root inputs, and inspect equivalent related-helper behavior. Preserve normal Filter source creation, editing, restoration, and removal. Run focused affected checks and adversarial review under AGENTS.md, then commit one coherent repair. Check current source first because other workers may already have changed these paths. Do not change the Goal or runtime.

## Pending operator decisions (recorded Round 62, 2026-11-02)

Safe default for every item: the current shipped behavior stays (all paths are fail-closed today); no program slice proceeds without an explicit answer. Full choice-consequence framing lives in the parent-owned untracked draft `/tmp/opencode/operator-decision-digest-draft.md` (cited, not copied). Ledger authority: `verification/publication-bundle/FINDINGS.md` Round 59 §1 (deferred-operator-question line, added Round 62) and Round 62 §3; worklist rows OPS-62-01/OPS-62-02.

1. **F-DS1 dedupe-bind vs named-422 (deferred).** Shipped (`62f627fb`, refusal-not-dedupe): a signed source slot for a root field already projected as a public `SourceField` is refused with a named 422 at `internal/explorer/lifecycle/derive_source.go:304`, `internal/explorer/lifecycle/filter_source.go:252`, and `internal/explorer/lifecycle/unpivot_source.go:344`, driven by the shared predicate at `internal/explorer/lifecycle/construction_pivot_source.go:658` (carrier set `:687`, Derive/Filter/Unpivot lanes). Keeping named-422 requires unprojecting the public column first and keeps the two user intents distinguishable under the receipt guard (`internal/server/explorer_output_reconcile.go:497-512`); dedupe-bind is a wider change (the signed lane becomes the same projection as the user column), not a relaxation of the legit same-column-reuse case (`internal/server/fs1_signed_slot_collision_test.go:323-346` probe6 case-D). Absent an answer: named-422 refusal stands.
2. **Capability choice-exclusion of user-projected fields (deferred).** Capability discovery does NOT subtract the user's already-projected public columns from the signed-choice list (enumeration at `internal/explorer/lifecycle/unpivot_source.go:161-175`; twins `internal/explorer/lifecycle/derive_source.go:137`, `internal/explorer/lifecycle/filter_source.go:94`); collisions are caught at propose time by decision 1. Excluding advertised-but-unbindable choices improves honesty of advertising but couples capability discovery to current document shape (options would flicker as the user projects/unprojects, and any later same-field relaxation would need per-edit refetch or would break optgroup restore). Absent an answer: advertise-then-refuse stays; discovery remains pure and document-shape-agnostic.
3. **EXPAND deferral ratification note.** EXPAND remains a later slice requiring an ARRAY-valued public column today (`NO_ARRAY_COLUMNS` at `internal/dataframe/compiler/lower/recipe_construction.go:2249`); an ARRAY-shaped signed projection admission class is a distinct shape contract from the frozen scalar signed-choice design (three-way shape anchors `internal/explorer/capability/row_choice.go:30-32`, validation at `:282`). Ratification should first record the array-projection contract decision (shape, repeated-boundary observation facts, value-type ledger) before any implementation slice opens. Absent an answer: the EXPAND source channel stays unimplemented and no array-shaped signed-choice design is drafted.
