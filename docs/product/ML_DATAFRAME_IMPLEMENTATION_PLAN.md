# Implement researcher-authored ML dataframes

Build collection selection, deliberate row grain, meaningful features, interpretation repair, and reproducible exports on Loom's existing execution path.
Use the [technical design](ML_DATAFRAME_TECHNICAL_DESIGN.md) for contracts and ownership, and the [gap analysis](BACKEND_GAP_ANALYSIS_20260916.md) for evidence.
Execute B01, then independent B02 and B03, then B04 through B08.
This plan supersedes the Patient-first F1-F4 proposal. It does not authorize implementation, pushing, deployment, or merging.

## How to read this

One box is one unit of work. Every box names the evidence required for completion. Check a box only when its evidence exists. The implementation issue IDs are stable and match the execution records under `ml-dataframe/`.

Use the installed `poteto-mode/playbooks/autopilot-stack.md` for execution, subject to the user's explicit overrides below. The foreground agent owns topology and final judgment. Luna workers implement bounded packages. Do not auto-merge. Execute sequential work on the integration line; create worktree branches only for genuinely concurrent B02/B03 work. An explicit execution go starts implementation, not unattended production deployment.

Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

Use focused executable tests for every issue. Run a live API probe for changes to storage or execution. Run the Docker/DOM journey at B01, B04, B05, B06, B07, and B08, rather than for every internal issue. Run the complete integrated suite once at B08. These instructions override the generic pstack ten-agent live swarm and mandatory human screenshot/video approval. No agent vote substitutes for executable evidence.

The existing `scripts/validate_architecture_plan.py` validates task-record structure, baseline references, dependencies, and existing-file ownership. It does not prove the design or future behavior.

## Program checklist

### Arm the program

- [ ] On explicit go, record the plan path, exact source SHA, assigned packages, and verification policy in the standing orders. Keep implementation evidence under `.audit/ml-dataframe-implementation/`.
- [ ] Reconcile `arch/integration` with source baseline `a921b9e5dca1d42a84a836286140fb3b4d704f3b`. If it moved, inspect the intervening diff before applying this plan. Do not implement from the older planning checkout.
- [ ] Read the execution playbook from the installed plugin, this plan, the technical design, and `.codex/skills/verify/SKILL.md`. Use the risk-based verification skill for behavior changes. Record any unavailable gate as blocked, not passed.
- [ ] Record the baseline test results, warm iteration timing, and currently active publication. Send a status message at work-package boundaries. A 30-minute audit tick is appropriate only if execution becomes unattended; do not invent an unavailable scheduler.

### Spawn owners

- [ ] Run B01 first. After its verified commit, run B02 storage/lifecycle work and B03 semantic/compiler work in separate worktrees if both workers are available. Otherwise execute them serially without extra branches.
- [ ] Give B02 ownership of selection persistence and selection-read orchestration. Give B03 ownership of correlated bindings, semantic checks, concept catalog, and compiler changes. The root alone integrates shared OpenAPI/generated files, receipt identity fields, lifecycle configuration, and shared acceptance fixtures. B02 submits any required authoring/compilation interface change to the root instead of editing B03's files.
- [ ] Join B02 and B03 before B04. Run B04, B05, B06, B07, and B08 sequentially because they change shared authoring/compiler contracts. Do not parallelize by ignoring those dependencies.
- [ ] Keep one root final review per coherent package. Use Luna for delegated work. Do not invoke Astra without the configured rescue trigger.

### PR mechanics

- [ ] Keep each issue's implementation and tests together. Record its exact implementation SHA, command, and evidence path in the execution records.
- [ ] Before any separately authorized push, run applicable generated-contract checks, `git diff --check`, and the package gate. Include deleted/replaced paths in the review description.
- [ ] Inventory all writable contract callers before changing commands. Migrate the UI, generated bindings, config conversion CLI, repository publication, fixtures, and examples in the same package.
- [ ] Use the root as the only integration/topology writer. Never overwrite another worker's dirty files or change the running Docker source mount between workers without recording it.

### Verdict and merge

- [ ] Accept a package only after its issue gates and package-level behavior checks pass at the recorded SHA. A source review alone cannot close a data-correctness issue.
- [ ] Reject a package that adds a parallel authoring model, business rules in HTTP handlers, raw-query escape hatches, duplicated extraction evaluators, or a new unsupported capability in the UI.
- [ ] Stop at verified commits or merge-ready changes according to the user's execution instruction. Only the user grants merge/deploy authority.

### Boot recipe

- [ ] Run `rtk proxy make dev`, then `rtk proxy make dev-doctor`. Require `DEV_DOCTOR_PASSED` for the isolated `loom-dev` stack on API 8180 and UI 3180. Never use the canonical `loom-demo` as the disposable test target.
- [ ] Reuse the mounted Go/Vite watchers. Use `rtk proxy make dev-rebuild` only for dependency, toolchain, or image changes. After an edit, prove the new code is serving before measuring behavior.
- [ ] Run `rtk proxy make verify-fast` at the specified journey milestones. Extend `scripts/loom-dev.mjs` with accessible-role/label assertions and literal expected values. Save its DOM, API, and artifact evidence under `.artifacts/loom-dev/<run-id>/`.
- [ ] Run `rtk proxy make verify-full` after changing watcher/driver mechanics and at final integration. Preserve original source files when the driver tests HMR and backend rebuild recovery.

## Unify authoring intent and correct output claims (B01)

**Depends on.** None.

**Files.**

- [ ] Extend `internal/explorer/authoringv2/{types,semantic_types,commands,canonical,migration}.go` and existing tests. Add proposed `feature_source.go` inside the same package.
- [ ] Update `internal/explorer/compilation/semantic_compile.go`, `internal/explorer/{compilation_receipt,output_contract}.go`, server contracts, generated bindings, and their callers. Change UI contract consumption in the existing client and Builder. Do not add `datasetdesign`.

**Build.**

- [ ] ML-B01-01. Replace writable flat source options with validated source variants. Retain `Document`, `Column`, stable column keys, and presentation. Add exact-meaning source-edit commands using the existing CAS/idempotency path. Establish `ResolvedInputs`, its digest in compilation/receipt identity, and optional immutable selection references for the next packages without advertising unimplemented operations. Prove direct value, count, and exists round trips.
- [ ] ML-B01-02. Separate field repetition from related-resource multiplicity in compilation. Derive shape from checked result type. Expose explicit loss reasons and structural suitability. Remove unconditional ML-ready/lossless claims for ambiguous related values. Prove `DISTINCT_VALUES` has array shape.
- [ ] ML-B01-03. Introduce a single mutable-draft migration with a semantics-version check. Preserve existing FIRST behavior as an explicit lossy ordering policy needing review before affected new publication. Keep immutable old receipts unchanged; reject unsupported execution with recompile-required. Test idempotent CAS persistence and stale clients.
- [ ] ML-B01-04. Replace old source-edit callers and assertions together. Add the hostile fixture and package-boundary guard described in Appendix A. Keep both guided and graph controls bound to the same document, not separate stores. Run the old ordinary-field journey successfully after migration.

**You see.**

- [ ] An ordinary existing table still works. The UI accurately labels array shape and lossy related-value selection. A feature source can be changed without losing its identity or presentation.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/explorer/... ./internal/dataframe/compiler/... ./internal/server ./cmd/explorer-config-v2-convert`. Add literal migration, command replay, related-FIRST, and aggregate-shape cases. Run `rtk proxy make openapi-check dataframe-boundaries` and `rtk proxy npm --prefix ui test`.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run the existing `verify-fast` journey before and after B01. Assert the same ordinary rows and the corrected contract for two related values. Record intentional contract differences instead of calling them regressions.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Warm save/reconcile/preview latency and edit-to-assertion time.
- [ ] Probe. Run five interleaved baseline/head repetitions on the same warm fixture after warmup.
- [ ] Baseline. Record the a921b9e measurement first; preserve its fixture and machine details.
- [ ] Rule. Investigate a repeatable median increase over 20 percent. Require the warm focused loop within the 30-second target or report the measured blocker.

**Review gate.** None. No mandatory operator interaction review. No routine human UI gate. Root checks the contract and migration evidence.

**Merge.**

- [ ] Record B01's verified SHA before branching B02/B03. Do not merge or deploy without authority.

## Freeze starting collections through existing storage (B02)

**Depends on.** B01. Can run alongside B03.

**Files.**

- [ ] Add proposed `internal/explorer/selection.go`, `internal/explorer/arango/selections.go`, and `internal/explorer/lifecycle/selection.go`. Extend existing store and collection bootstrap contracts.
- [ ] Extend `internal/dataframe/published` for exact-execution resolution and addressable source-row metadata. Add reader retention to `internal/dataframe/publication/{bundle.go,arango/bundle_registry.go,clickhouse/bundle_store.go}`. Reserve shared server/OpenAPI integration for the root. Do not modify B03 compiler files.

**Build.**

- [ ] ML-B02-01. Implement immutable selection headers and streamed membership records with canonical project, generation, typed resource identity, digest, and complete-state validation. Add unique lookup indexes and bounded staging cleanup. Test retries, duplicate members, empty selection, and incomplete writes.
- [ ] ML-B02-02. Resolve checked resources and all-matching published-output selections with exclusions. Persist exact source execution/revision/receipt/output identity. Add and acquire renewable reader pins in the publication catalog before scanning; make cleanup honor them and cancel on pin loss. Use typed published filters, never serialized GraphQL/AQL. Reject outputs without a proven resource-identity mapping.
- [ ] ML-B02-03. Validate authorization and generation on creation, header/member reads, attachment, and reuse. Reject changed effective scope before revealing counts; bind cursors to scope and revision. Make reselection create a new revision. Bind selection identity into resolved inputs and receipt/publication identity. Test stale scope, cross-project references, current-pointer advancement, and membership changes.
- [ ] ML-B02-04. Add typed selection create/read endpoints and integration tests. Preserve rule plus actual membership. Return explicit limits and source-addressability errors. Do not claim population authoring is complete until B04 connects this storage to compilation and controls.

**You see.**

- [ ] The API can freeze selected rows across pagination and exclusions. Repeating the same pinned request produces the same membership digest; later publication cannot change it.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/explorer/... ./internal/dataframe/published/... ./internal/dataframe/publication/... ./internal/server`. Add storage immutability/CAS, reader-pin/cleanup-race, and source-addressability tests. Confirm a failed membership stream never returns a usable revision.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Through the owned local API, select three files and exclude one. Read back exactly the two expected typed identities after API restart. Advance the published source and prove the saved selection remains unchanged. No browser run is required for this internal storage package.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Selection throughput, total time, RSS, and staged bytes.
- [ ] Probe. Create selections of 100, 10,000, and 100,000 fixture references and exercise configured limits.
- [ ] Baseline. Baseline has no saved-selection operation. Record source-reader throughput separately; do not claim an end-to-end speedup.
- [ ] Rule. Require bounded batches, exact membership, and explicit failure at row/byte/time limits. Reject proportional whole-membership memory buffering. Target a small warm selection request within 30 seconds.

**Review gate.** None. No mandatory operator interaction review. No routine human gate. Root checks security, failure visibility, and ownership.

**Merge.**

- [ ] Integrate the B02 worktree without B03 file edits. Re-run its focused gate on the combined B02/B03 head before B04.

## Preserve correlated FHIR concept values (B03)

**Depends on.** B01. Can run alongside B02.

**Files.**

- [ ] Extend `internal/catalog`, `internal/explorer/capability`, `internal/fhir/schema`, `internal/dataframe/{spec,expression,semantic}`, and `internal/dataframe/compiler/{ir,lower,render/aql}`.
- [ ] Extend feature-binding validation in `authoringv2` and compilation. Use new focused files where appropriate; do not grow all binding cases inside `semantic_compile.go`. Root integrates shared generated contracts.

**Build.**

- [ ] ML-B03-01. Preserve system/code identity, extension ancestry, choice arm, unit observations, and evidence completeness in catalog concept candidates. Expose supported and unresolved candidates without fabricating friendly equivalence.
- [ ] ML-B03-02. Extend typed correlated predicates/extraction so system and code bind within one Coding item and its owning repeated component. Reuse `PhysicalPivotMap` and scoped expressions. Prove the same pairing behavior for filters and projected values.
- [ ] ML-B03-03. Replace hard-coded string-coalescing lookup behavior with validated bindings and declared result types. Preserve raw values and produce typed invalid/mixed-arm outcomes in execution, without waiting for B07's reporting UI. Keep unmatched data accessible. Avoid adding a second Go evaluator.
- [ ] ML-B03-04. Add cross-element, colliding-code-system, nested-extension, and mixed-choice tests through semantic lowering and real Arango execution. Add candidate/receipt lineage assertions that preserve exact structural binding.

**You see.**

- [ ] Two equal code strings in different coding systems remain distinguishable. Component B's value cannot populate component A's feature. An unknown binding is visible, not silently dropped.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/catalog/... ./internal/fhir/schema/... ./internal/dataframe/spec/... ./internal/dataframe/semantic/... ./internal/dataframe/compiler/... ./internal/explorer/capability/... ./internal/explorer/compilation/...`. Add literal paired-value expectations, not only rendered-query assertions.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Execute the correlated fixture through receipt Preview on the owned stack. Assert discriminating literal values plus binding/choice metadata for both coding systems and components. Repeat with a missing system and incompatible choice arm. Per-cell contribution reporting arrives in B07; no full browser journey is required here.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Paired-binding extraction latency, allocations, and scan count with tracing off.
- [ ] Probe. Run direct-field baseline/head cases and the new paired fixture with identical row counts.
- [ ] Baseline. Record direct-field behavior and timings first. The paired semantics are new and have no equivalent old baseline.
- [ ] Rule. Investigate a repeated direct-field median regression over 20 percent. Reject per-feature full-dataset scans; require the small paired preview within the 30-second warm iteration target.

**Review gate.** None. No mandatory operator interaction review. Root reviews literal pairing evidence before mapping controls can depend on it.

**Merge.**

- [ ] Integrate B03 and B02; verify shared contracts once on the combined head. Advance only after both semantic and membership probes pass.

## Separate selected populations from row grain and feature scope (B04)

**Depends on.** B02 and B03.

**Files.**

- [x] Extend authoring commands and row intent, `explorer/compilation`, recipe output constraints, semantic planning, membership lowering, and compiler scope validation.
- [x] Add proposed `population.go` and `row_change.go` in the relevant existing packages. Wire collection and row controls into the existing Builder and Viewer selection handoff.

**Build.**

- [x] ML-B04-01. Compile the immutable selection as a typed membership semijoin, retaining target row grain. Test direct and reversed paths, shared target resources, missing links, and exact selection-to-row trace mapping. Preserve project/generation/auth constraints at every step.
- [x] ML-B04-02. Replace root reset with assess-and-apply row changes. Preserve selection and stable features when rebasing is unambiguous. Return explicit affected-feature errors otherwise. Apply atomically under the existing draft digest.
- [x] ML-B04-03. Allow independent relationship occurrences with contributor predicates, while retaining route bounds. Distinguish population eligibility from optional feature matching. Update traversal-sharing identity to include semantic scope and predicates.
- [x] ML-B04-04. Add checked/all-matching/exclusion controls and the row-definition control using the existing command queue. Show selection size, resulting rows, and unmapped sources. Prove a file-to-specimen journey and a non-file-root journey without Patient.

**You see.**

- [x] Selecting F1 and F2 yields one specimen S1, with both files still traceable. Adding unrelated files does not enlarge this saved selection. Changing rows does not silently erase features.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Run `rtk proxy go test ./internal/explorer/... ./internal/dataframe/recipe/... ./internal/dataframe/semantic/... ./internal/dataframe/compiler/... ./internal/server` and `rtk proxy npm --prefix ui test`. Include separate predicates through the same relationship and failed rebase preserving the exact old draft digest.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Extend and run `verify-fast` for file selection, shared-specimen deduplication, explicit empty/unmapped handling, independent feature scopes, and a Specimen-root start with no DocumentReference requirement. Assert API membership and DOM row values, then reload.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [x] Metric. Population semijoin scans, latency, and row-change-to-preview time.
- [x] Probe. Compare target-scan and membership-driven plans at sparse/dense selection ratios after literal row equality.
- [x] Baseline. Record the existing equivalent manual recipe where possible and identify the new membership work separately.
- [x] Rule. Require indexed scoped membership access and exact target deduplication. Require the small warm row-change journey within 30 seconds or block on the measured plan problem.

**Review gate.** None. No mandatory operator interaction review. Root checks that no selection/feature is silently discarded. No routine manual click-through.

**Merge.**

- [x] Record the first complete selection-to-row user journey and its exact receipt before B05.

## Author deliberate reductions, time windows, and units (B05)

**Depends on.** B04.

**Files.**

- [ ] Extend feature policy types, existing recipe aggregates/slices, `dataframe/expression`, semantic checks, physical ordering/reduction, and output contract derivation.
- [ ] Add focused feature-editor components under the existing `ExplorerBuilder` directory. Keep FHIR policy out of UI reducers and HTTP handlers.

**Build.**

- [ ] ML-B05-01. Expose require-one, collect, distinct, count, count-distinct, exists, min, max, and contains-all with exact resource-versus-value count semantics. Separate related-record reduction from nested field projection. Preserve nulls and association evidence. Enforce require-one and invalid-transform failures during execution now, using existing materialization failure handling; do not wait for B07's aggregate quality reports to prevent unsafe activation.
- [ ] ML-B05-02. Implement latest/earliest/first-ordered using explicit timestamp, anchor, bounds, inclusivity, precision, and tie policy. Extend typed IR and rendering together. Unknown anchors and unsupported precision cannot silently select a record.
- [ ] ML-B05-03. Implement approved unit normalization with dimensional checks and pinned conversion rules. Retain original value/unit in evidence. Unknown or incompatible units remain unresolved. Test identity, linear, and affine conversions without implicit display-label matching.
- [ ] ML-B05-04. Replace remaining `WherePath`/`WhereEquals` writes with typed contributor predicates. Wire feature controls and exact-meaning summaries. Reject unsupported combinations before save; leave valid but data-ambiguous definitions editable and block unsafe publication with explicit reasons.

**You see.**

- [ ] A researcher chooses how several matching measurements become one feature. A latest-before-anchor feature returns the expected value and unit; ties and missing anchors have visible reasons.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/explorer/... ./internal/dataframe/expression/... ./internal/dataframe/semantic/... ./internal/dataframe/compiler/... ./internal/server` and UI tests. Assert empty-set count/exists, null handling, timestamp boundaries, equal-time ties, choice arms, and exact converted values.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Extend `verify-fast` to author two differently scoped features through the same relationship, change reduction, choose a time window, preview, publish, and compare Viewer values. Include a deliberate ambiguous case that cannot publish as resolved.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Ten-feature preview latency and traversal count.
- [ ] Probe. Interleave existing feature baseline/head queries; separately run the new reduction/time/unit fixture.
- [ ] Baseline. Record old supported-feature timings before changes. Do not compare unlike old/new temporal scenarios.
- [ ] Rule. Investigate a repeated existing-path median regression over 20 percent. Require exact values under sharing and small-fixture new-policy preview within the 30-second warm loop.

**Review gate.** None. No mandatory operator interaction review. Root checks semantics and the absence of duplicate evaluators.

**Merge.**

- [ ] Record verified source-policy behavior and deleted legacy filter writes before B06.

## Save and apply reusable interpretation revisions (B06)

**Depends on.** B05.

**Files.**

- [ ] Add proposed `explorer/interpretation.go`, `explorer/arango/interpretations.go`, and `explorer/lifecycle/interpretation.go`. Extend existing authoring references, receipt normalization, server routes, and capability lookup.
- [ ] Add interpretation proposal/review controls inside the feature editor. Use existing recipe fragments only after resolving authoring meaning.

**Build.**

- [ ] ML-B06-01. Persist immutable interpretation revisions with content digest, applicability, parent, author, and explanation. Use existing Arango immutable-insert/CAS conventions. Do not store researcher mappings in ingestion configuration or fake recipe bundles.
- [ ] ML-B06-02. Resolve exact revisions before compilation and freeze resolved definitions in the receipt's resolved inputs and recipe identity. Retain exact references in the workspace and cover resolved content through `ResolvedInputsDigest`. Explicitly select human definitions over suggestions; overlapping rules require priority or produce ambiguity. Never load a moving library head during execution.
- [ ] ML-B06-03. Implement candidate-revision preview against a candidate receipt, compare changed values and unresolved cases, and apply by CAS only when explicitly requested. Leave source FHIR, the current draft, and other consumers unchanged during proposal.
- [ ] ML-B06-04. Add browse/apply/create-revision controls with raw-value access and affected-record preview. Show sample/completeness status. Test that updating a library does not alter an existing dataset until its pinned revision changes.

**You see.**

- [ ] Fixing one recurring mapping can resolve several records. The interface shows exactly which values change, retains unmatched data, and leaves another dataset using the older revision unchanged.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/explorer/... ./internal/server` and UI tests. Assert immutable revision collision behavior, applicability mismatch, overlap ambiguity, CAS conflict, resolved digest changes, and old receipt stability.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Extend `verify-fast` to preview a mapping repair, cancel without changing the draft, then apply it and reload. Compare raw source values and another pinned dataset before/after. Verify unauthorized library access is denied.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Cold/warm interpretation resolution and bounded difference-preview latency.
- [ ] Probe. Resolve one revision across repeated features and run candidate comparisons with configured scan limits.
- [ ] Baseline. Record prior inline-definition reconcile time; library lookup is newly added work.
- [ ] Rule. Require at most one resolution per distinct pinned revision per reconcile. Small-fixture comparison must fit the 30-second warm loop; exceeded full-scan limits must report incomplete.

**Review gate.** None. No mandatory operator interaction review. Root checks revision isolation and the distinction between preview and apply.

**Merge.**

- [ ] Record a verified reuse-and-repair journey and preserved source digest before B07.

## Explain cells and gate publication with complete evidence (B07)

**Depends on.** B06.

**Files.**

- [ ] Extend `dataframe/execution` with optional typed evidence sinks and compiler evidence projections. Add proposed `explorer/evidence.go`, `explorer/lifecycle/evidence.go`, and immutable evidence persistence in the existing Arango adapter.
- [ ] Extend lifecycle publication validation and the feature/Preview/Viewer evidence controls. Retain the existing atomic release activation path.

**Build.**

- [ ] ML-B07-01. Produce cell status and contribution evidence from the same compiled operators as values, before lossy reductions. Implement targeted row/feature trace with bounded pagination. Do not store every cell's full provenance in every Preview response.
- [ ] ML-B07-02. Compute full-population quality separately from bounded Preview. Create reports only after receipt identity exists, and bind them to receipt, generation, scope, output, and check-policy version. Never include their future digest in the receipt. Include completeness, limits, key integrity, coverage, ambiguity, invalid units/types, and explicit omissions.
- [ ] ML-B07-03. Gate activation on invariants checked during the materialization stream. Keep prior publication active after cancellation or quality failure. Never retrofit a report digest into an immutable receipt or use sampled evidence as full approval.
- [ ] ML-B07-04. Add reason-specific repair links and a cell explanation panel using one server-owned descriptor. Keep ordinary users out of FHIR details unless they expand them. Distinguish no observation from a recorded null and from ambiguous data.

**You see.**

- [ ] The same cell value and contributing record appear in Preview, trace, publication, and Viewer. A timeout says incomplete. A failed check cannot replace the current published dataset.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./internal/explorer/... ./internal/dataframe/execution/... ./internal/dataframe/compiler/... ./internal/dataframe/publication/... ./internal/server` and UI tests. Assert stream cancellation, missing evidence, mismatched identities, restricted-empty authorization, and activation failure behavior.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Extend `verify-fast` to inspect a value and each missing-value reason, focus a repair, recheck, and publish. Force an incomplete full check and an activation failure. Assert the former publication stays readable and no hidden-record counts leak.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Tracing-disabled latency, targeted-trace latency, full-scan throughput, RSS, and evidence bytes.
- [ ] Probe. Compare tracing off, one-cell trace, and full quality scan on the same receipt fixture.
- [ ] Baseline. Record existing preview time and memory before enabling evidence generation.
- [ ] Rule. Investigate over 20 percent repeatable overhead with tracing off. Require bounded trace pages and no full-data buffering. Full scans must honor configured time/memory limits and cannot report complete after timeout.

**Review gate.** None. No mandatory operator interaction review. Root checks identity, security, and exact value/evidence agreement.

**Merge.**

- [ ] Record complete and failed publication evidence at the verified head before B08.

## Export a pinned artifact and verify the integrated product (B08)

**Depends on.** B07.

**Files.**

- [ ] Add generic stream/archive encoding inside `internal/dataframe/published` and Explorer manifest/completion orchestration in proposed `internal/explorer/lifecycle/export.go`. Reuse exact execution reads and reader pins introduced by B02. Extend server routes and existing download controls.
- [ ] Extend `scripts/loom-dev.mjs`, driver tests, and `.codex/skills/verify` feature coverage. Regenerate OpenAPI bindings from their source; never hand-edit generated contracts.

**Build.**

- [ ] ML-B08-01. Resolve one published revision/output using existing `BundleCatalog.GetExecution` and validate all identities. Acquire, renew, and release the B02 read-retention pin during preparation; abort on pin loss or unavailable materialization. Never fall back to latest-by-selector.
- [ ] ML-B08-02. Stream data and metadata into a bounded temporary artifact, finalize checksums, and expose only complete downloads. Include selection membership, interpretation revisions, schema, quality, provenance, and null/array encoding. Handle cancellation, storage limits, restart, expiry, and authorization recheck.
- [ ] ML-B08-03. Replace whole-dataset browser buffering with the server artifact path. Keep existing table paging. Test concurrent republish, later-page failure, null versus empty-string round trip, and explicit arrays. No partial artifact appears complete.
- [ ] ML-B08-04. Run the complete integrated suites and user journeys. Update verification skill coverage and record unsupported/deferred capabilities. Close tasks only with implementation SHA and evidence. Verify that no duplicate authoring path or retired writable source representation remains.

**You see.**

- [ ] A researcher downloads exactly the table they checked, with its definitions and source membership. Publishing B during export A cannot change A. A failed export remains a failure.

**Verify, unit.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run `rtk proxy go test ./...`, `rtk proxy make openapi-check graphql-check dataframe-boundaries`, `rtk proxy npm --prefix ui test`, UI/demo builds, and `rtk proxy node --test scripts/loom-dev.test.mjs`. Run generator consistency checks for every changed generated contract.

**Verify, live.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Run extended `verify-fast`, then `verify-full`. Unpack the downloaded artifact and compare literal rows, member checksums, scope, membership, and revision identities. Exercise concurrent publication and failed preparation through the real API. Preserve reports and parsed members, not only screenshots.

**Verify, perf.** Tests alone are not sufficient verification. A PR is verified only when its unit, live, and perf boxes are all checked.

- [ ] Metric. Artifact preparation time, server/browser memory, archive bytes, and warm watcher timings.
- [ ] Probe. Run small and multi-page downloads, concurrent republish, injected failure, and verify-full.
- [ ] Baseline. Record current CSV export memory/time and existing HMR/Go rebuild timing first.
- [ ] Rule. Reject browser memory growth proportional to the whole dataset and partial-success artifacts. Record full export latency separately from the 30-second warm edit-to-assertion target.

**Review gate.** None. No mandatory operator interaction review. Root provides one final integrated verdict. The user retains merge/deploy authority.

**Merge.**

- [ ] Deliver verified commit/branch identities, completed task records, known limits, and real evidence. Do not claim performance gains without baseline/head measurements.

## Close the program

- [ ] Mark every implementation issue with its verified SHA and evidence. Keep failed or unsupported capabilities blocked rather than silently removing their acceptance criteria.
- [ ] Re-run the plan-record validator. Refresh GitNexus after source changes. Preserve old immutable publications and the pre-migration draft backup.
- [ ] Deliver the integrated product with its acceptance report. No manual triple-check by the user is required for the automated acceptance scenarios.

## Appendix A. Prototype evidence

No implementation prototypes were run while writing this plan. The prior investigation passed six focused baseline tests and confirmed the source limitations. The technical design lists the remaining executable design gates and their owning packages.

Use one small, hostile fixture in the existing verification mechanism:

- F1 and F2 both map to S1; F3 maps to S2; F4 has no specimen. Selecting F1/F2 produces S1 once and a two-file trace. Excluding F2 leaves S1 once with one file.
- S1 has two observations at distinct timestamps and a third with an equal timestamp for tie tests. S2 has none. Include a value outside the window and a missing anchor.
- One Observation contains two components and several Coding items. Use the same code string in two systems with different values. Assert exact same-item pairing.
- Include nested extensions with the same leaf URL under different parents, numeric/string choices, compatible and incompatible units, empty strings, explicit nulls, absent paths, and an unknown concept.
- Include a second project and restricted authorization paths. Every selection, trace, quality report, and artifact must respect the same effective scope.

Author the final fixture values with literal expected matrices before implementing each behavior. Do not generate expected results using the compiler under test. Preserve the existing Patient fixture as regression coverage, not as a product restriction.

## Appendix B. Alternatives rejected

- A parallel wizard model and adapter would duplicate authoring intent and create a permanent compatibility path. Evolve the current model instead.
- Putting researcher interpretations in ingestion configuration would conflate source loading with dataset meaning and force re-ingestion for feature changes.
- A generic raw-expression editor would expose implementation freedom instead of the bounded decisions users need. Use typed feature policies and explicitly supported operations.
- A separate profiler/evaluator would risk disagreeing with materialization. Derive evidence from the compiled execution path.
- Treating code labels as concept identity would conflate different coding systems. Preserve structural and terminology identities.
- Running a browser swarm for every internal issue would discard the existing fast focused loop. Use package-appropriate checks and complete-journey gates.

## Appendix C. Risks

- B02/B04 membership performance and source-row addressability need runtime proof. A failed proof blocks the affected path, not permission to inject unbounded IDs.
- B03/B05 correlated values, mixed types, and temporal precision can create convincing but incorrect features. Literal hostile fixtures are release gates.
- B01 migration changes mutable semantics. Preserve raw backup data, require supported client versions, and prove idempotence. Never downgrade new drafts by discarding fields.
- B06/B07 evidence and mappings may contain sensitive data. Scope them by project/authorization and minimize retained samples.
- B08 inactive materialization cleanup can invalidate an export. Implement a real retention pin or copy before cleanup; a stale ID is not protection.
- Warm iteration under 30 seconds is a target, not a promise that a full dataset check finishes in 30 seconds. Report both separately.
- Expand-to-many row authoring, SUM/AVG, arbitrary formulas, imputation, encoding, splitting, Parquet, and model training remain follow-up capabilities. Reject unsupported requests explicitly.

## Appendix D. Links and reading list

- [Technical design and ownership](ML_DATAFRAME_TECHNICAL_DESIGN.md).
- [Backend gap evidence](BACKEND_GAP_ANALYSIS_20260916.md).
- [Execution issues](ml-dataframe/ISSUES.csv) and [work-package records](ml-dataframe/WORK_PACKAGES.csv).
- `docs/PACKAGE_AUDIT.csv` and `scripts/check_dataframe_package_boundaries.sh` for existing boundaries.
- `.codex/skills/verify/SKILL.md` and `scripts/loom-dev.mjs` for live verification.
- Installed pstack `how`, `principle-model-the-domain`, `principle-migrate-callers-then-delete-legacy-apis`, `principle-sequence-verifiable-units`, and `principle-prove-it-works` skills. Use specialized architect/interrogate escalation only for a material unresolved design, not every package.
