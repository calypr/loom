# C01–C12 implementation runbook

Revision 3. Read the [delivery plan](../ML_DATAFRAMER_DELIVERY_PLAN.md) for user outcomes, [contracts](CONTRACTS.md) for cross-cutting decisions, and [acceptance protocol](ACCEPTANCE.md) for release journeys. The [ledger](execution.json) remains the status authority. Nothing below is implemented or accepted merely because it appears here.

## How to read this

Each package has a before/after user task, implementation sequence, failure behavior, and a closure test. The four ledger units are ownership-sized units, not four arbitrary commits or four backend phases. Where a unit is large, use the smaller checkpoints here. At every checkpoint, keep its test next to its behavior. Do not postpone all UI work until the backend is complete.

Source paths below are existing seams, not promises to put all new code in those exact files. Split cohesive responsibilities into neighboring files when necessary; do not create a C-package backend namespace or append every handler to BuilderWorkspace. New wire operations are defined in OpenAPI alongside existing V2 operations. Tests named as proposed are work to add, not commands that already pass.

## Program checklist

- [ ] Record the implementation branch, base SHA, working-tree changes and Docker target. Preserve the populated data volume.
- [ ] Start C01 only after R1/R2 have executable answers. A bounded spike is part of C01, not a new product tranche.
- [ ] Use the existing isolated Vite/backend-watch stack. Read its project-local verification skill and run its target/health checks before browser work. Do not assume port 30002 identifies the intended source checkout.
- [ ] Run focused changed-package tests and a narrow DOM/output probe during iteration. Measure warm edit-to-assertion latency; target median ≤30 seconds over five repetitions, excluding initial ingest/build.
- [ ] Close each C package with its stated user journey. Do not run the entire CDA release suite for every helper change. Run the integrated release suite after the combined tranche, and sooner only when a change affects a shared execution invariant.
- [ ] One implementation worker at a time by default; root owns judgment/integration. Use one coherent final review, not separate reviewers for every test and document.
- [ ] Update ledger evidence, exact SHA, observed KPI values and remaining limitations. Planned targets and prototype results cannot mark product KPIs passed.

## C01 — Browse concepts and add only selected columns

Before: the user navigates resource occurrences and technical fields. After: they open a new Specimen- or Observation-rooted table, search observed concepts, add three, and obtain only those columns in an artifact.

Ownership: catalog domain and `catalog/arango/fields.go`; capability read model; lifecycle authoring; pure `authoringv2/commands.go`; BuilderWorkspace save queue, ColumnSelector replacement and demo entry.

Checkpoints:

1. **Inventory proof (C01-02, R1).** Construct 1,000 distinct codes at the same profiled field, including repeated owners, duplicates and noncoding fields. Demonstrate current truncation. Implement keyed inventory with generation checkpoints and explicit completeness. Kill/retry the scanner; independently count unique binding groups and occurrence totals. Check ACL scope before choosing global versus scope-bound counts. Add cursor/query-plan tests before the browser catalog.
2. **First visible slice (C01-01/03).** Create a new table through the normal entry, select eligible rows and fetch the first server page. Add one recognized direct scalar through the existing save/reconcile path. Show source/system and type without a mandatory editor. Preserve old configured workspaces; no startup rewrite.
3. **Atomic multi-select (C01-03, R2).** Add the server-resolved semantic command described in CONTRACTS §4, with internal route allocation and semantic duplicate identity. Resolve decisions without mutation, then commit one CAS operation. UI basket spans pages and queries. Test unknown network outcome with the same command ID. Do not guess private occurrence IDs or save half the graph first.
4. **Closure (C01-04).** Browse beyond code 512, select three across pages/search, cancel a required choice, then resolve it and add. Independently read committed workspace and exported values. Repeat Add and replay the command; no duplicates. Reject a stale context and a forbidden source. Leave selections recoverable after failure.

Tests to add: store paging across stable cursor ties; inventory interrupted scan; scope-filtered facets; same-code/different-system and binding variants; no implicit scalar reduction; route+column atomic failure; semantic duplicate versus requested second output; basket query/page/cancel/CAS behavior. Existing atomicity tests are useful reuse evidence, not proof of the new command.

Finish line: J01 and K-C01-a/b/c, including 30 measured warm catalog queries. A fast first page from a silently incomplete 512-entry list fails. C01 delivers the first usable increment before definition import is complete.

## C02 — Find and freeze the records to include

Before: population attachment relies on a preexisting selection. After: the user browses raw files or other resources, chooses matching records and exclusions, freezes membership, and sees how those records map to rows.

Ownership: `explorer/selection.go`, lifecycle selection/population mapping and Arango selection store; PopulationPanel and a source-record picker in the same Builder feature area.

Checkpoints:

1. **C02-01.** Add an authorized cursor-paged raw-resource browse source alongside the existing published source. Use supported typed filters and safe field descriptors, not arbitrary AQL. Picker labels fall back to resource IDs. Return source-generation/scope/query identity and selection counts, not every matching ID.
2. **C02-02.** Freeze explicit IDs or all-matching-minus-exclusions server-side. Bind the evaluated query and generation; validate every selected member. Membership is immutable. Mutable collection naming points to a revision and cannot change its members. Interrupted creation must not expose a complete revision.
3. **C02-03.** Attach through the UI, choose target row type and meaningful route, display selected-source/mapped-source/distinct-row counts separately. Preserve directionality in execution. Load further unmatched examples rather than treating the first page as the entire unresolved set.
4. **C02-04.** Prove files 001/002/004 → two mapped files, one unmatched file, one specimen. Excluding 002 changes mapped count but not that specimen. A file-free Specimen collection also passes. Test cross-page and all-matching exclusions on a generated larger fixture.

Failure behavior: changed source generation/scope cannot freeze against stale browse context; revoked access yields no partial attachment. Cancel leaves current population intact. Changing population invalidates checks and catalog resolution, not pinned historical publications.

Finish line: J02 and exact membership/count oracles. A host integration in another repository is not required; the in-repo picker must actually work.

## C03 — Recognize more concepts from definitions

Before: only existing schema-recognized pairs are selectable. After: importing a supported definition enriches names/bindings, makes previously unresolved data discoverable, and adds no columns.

Ownership: ingest source-schema orchestration, pure `fhir/schema`, catalog semantic facts, capability descriptors and catalog UI. New import orchestration must remain separate from compiler extraction.

Checkpoints:

1. **C03-01, R3.** Inventory the actual definitions/terminology available for CDA and the fixture. Record canonical URLs, versions, dependencies, digests, license/distribution and supported subset. Stage/import locally without arbitrary instance-URL fetches. Failed/incompatible imports do not replace the active set.
2. **C03-02.** Implement base/component and explicit simple-extension support first. Add nested-extension support only with exact ancestry fixtures. Feed resulting checked binding types into existing compilation. Profile constraints beyond the supported subset remain visibly unsupported. Labels and equal code strings never create cross-system equivalence.
3. **C03-03.** Add recognition status/provenance to catalog search, plus a permission-appropriate import/retry action. Refresh metadata over retained inventory or a resumable raw scan. A selected column remains pinned when an imported label or binding changes.
4. **C03-04.** Measure group and occurrence coverage independently. Include unresolved no-code structures in accounting without classifying ordinary direct fields as broken. Test conflicts, missing package dependency, unavailable terminology and unsupported slicing.

Finish line: J03, supported fixture cases all recognized without manual pairing, and no unsupported case falsely Add-ready. CDA recognition percentage is measured after scanning; it is not a promised arbitrary target. A licensed terminology snapshot may be user-supplied; its absence must not block the basic open-fixture workflow.

## C04 — Inspect and customize selected columns

Before: construction exists in backend intent but is hard to inspect/edit. After: a user sees the exact route and contributors for a selected cell and can change their chosen output rule.

Ownership: FeaturePolicyEditor/construction inspector, `authoringv2/semantic_types.go`, compilation, compiler reduction lowering, lifecycle cell trace. Derive the focused graph from canonical intent; do not persist another graph model.

Checkpoints:

1. **C04-01.** Open inspector from a selected column. Show named links/directions, code+system, repeated owner, extracted type/unit and reduction. A worked row must come from executed evidence bound to the draft/receipt; illustrative examples are labeled and cannot impersonate trace results.
2. **C04-02.** Reuse the same route/policy controls for add-time ambiguity, without making all users open the inspector. Changing contributors must not change row population. Batch policy application lists compatible affected columns and excludes incompatible ones before submission.
3. **C04-03.** Expose only compiler-supported operations. Add absent mean/sum through semantic validation and the existing lowerer, with explicit numeric/empty behavior. A second count output requires an explicit action. Retain stable independent column IDs; no hidden shared mutable policy object.
4. **C04-04.** Execute zero/one/many contributors, same-valued distinct records, alternative routes and tied ordering. Match explained contributors to actual output. Test failed policy edit atomically preserves the prior feature.

Sequence detail: the C04 fixture establishes route → code matches → chosen output. C05 adds user-authored temporal filtering and its extra contributor stage. Do not make C04 depend on a later time-window editor.

Finish line: J04 with literal trace/output comparisons and ready-scalar selection still requiring zero inspector visits.

## C05 — Time windows and units

Before: policies exist but are incompletely exposed and explained. After: a user requests a result within an explicit window and knows which measurements were excluded or converted.

Ownership: FeaturePolicyEditor; backend capability enumeration, authoring policy types and existing semantic/compiler temporal/unit path. UI must not hardcode a capability the backend cannot execute.

Checkpoints:

1. **C05-01.** Author event time, anchor, lower/upper bounds and inclusivity. Mark missing timestamps and date-only/instant differences. Express latest/earliest ordering and tie policy explicitly; do not use database iteration order.
2. **C05-02.** Serve supported unit operations from capability metadata. Retain observed unit, target unit and conversion evidence. Reject incompatible dimensions and ambiguous source units. Do not infer units solely from a terminology label.
3. **C05-03.** Explain stages on a worked row: nine related records → four code matches → two in-window contributors → one chosen result. Unit normalization happens before reductions that require comparable magnitudes.
4. **C05-04.** Test bound equality, missing anchor, offsets/date precision, ordering ties, mixed convertible units and incompatible units against literal values. The same policy appears in preview, trace, Check and artifact metadata.

Finish line: J05. Unsupported temporal shapes remain clear errors, not best-effort coercion. No population membership changes unless the user separately applies a row filter.

## C06 — Resolve raw source structures into catalog entries

Before: interpretation review requires a recognizable candidate/column. After: a user opens unresolved data before any columns exist, defines a missing pairing, tests it, saves it and finds the resulting concept in the catalog.

Ownership: raw catalog inventory, `fhir/schema/correlated.go`, interpretation domain/store and lifecycle preview, BindingReview and interpretationCandidate replacement. Reuse interpretation revision ownership rather than creating a mapping registry beside it.

Checkpoints:

1. **C06-01, R4.** Group unresolved raw structures by profile/owner/full ancestry/reason. Include one fixture with no initial concept candidate. Differentiate missing metadata, ambiguous binding and unsupported extraction; offer definition retry before manual work where applicable.
2. **C06-02.** Let the user choose observed key/value nodes and repeated owner. Validate a closed binding type. Missing-system matching is explicit and scoped; it cannot stand for all systems. Show contrast examples from neighboring owners/extension branches.
3. **C06-03.** Implement no-column preview by compiling an ephemeral internal workspace. Bind preview digest to source/definition/mapping context. Save an immutable interpretation revision only after revalidation; expose catalog eligibility without adding a column. Show conflict rather than choosing newest rule silently.
4. **C06-04.** Through the DOM, resolve the no-candidate structure, find it in the catalog, add it and export. Prove A/shared=111, B/shared=222, missing-system=333 remains separate, and left/right nested branches do not cross. Compare raw-source hashes before/after.

Failure behavior: cancel/stale/invalid preview saves nothing; examples require authorization; a mapping that the compiler cannot represent stays unsupported. Existing pinned columns do not change when a new interpretation is approved. Revert means select a previous revision for future use, not mutate history.

Finish line: J06, including zero columns immediately after mapping approval. This is a complete workflow, not just an unmatched-resources list.

## C07 — Recode result categories

Before: the user can identify values but cannot consistently map study categories. After: they map exact typed values, see unknowns and choose an explicit unknown policy.

Ownership: interpretation feature definition, authoring semantics, compilation and existing expression lowerer; a category editor distinct from BindingReview.

Checkpoints:

1. **C07-01.** Add a closed exact typed mapping with declared output categories and unknown policy. Validate duplicate/overlapping keys and incompatible result types. Preserve extraction → reduction → recoding order.
2. **C07-02.** Show observed values/count scope and author case-sensitive alternatives explicitly. Unknown handling is explicit category plus typed raw companion, missing with diagnostic, or block. The companion output is disclosed before apply, not a surprise column.
3. **C07-03.** Compile through the existing checked expression path; carry revision and before/rule/after evidence to trace and artifacts. No formula language or transform service.
4. **C07-04.** Test `case`, `CASE`, `control`, empty string, null, numeric versus textual equivalents and unexpected values. Preserve row membership and raw source. Preview cancel/revert and pinned reuse must be exact.

Finish line: J07 and reconciled mapped/unmapped/missing counts. A recoding operation cannot silently filter rows.

## C08 — Declare the ML output contract

Before: a table has columns but no explicit X/y/ID distinction. After: the user declares roles, missingness and representation, and sees why a feature can or cannot enter a numeric matrix.

Ownership: `authoringv2.Column`, migrations/canonicalization, output contract, compiler representation and published artifact descriptors; DataframeContractPanel/feature controls.

Checkpoints:

1. **C08-01.** Add closed roles and representation variants. Migrate old columns to undeclared, preserving keys. Keep physical output slots tied to their authored feature, including explicit one-to-many encodings.
2. **C08-02.** Expose keep-missing, missing indicator and explicit constant replacement. Distinguish absent/null/invalid/ambiguous/incompatible-unit diagnostics. False and zero are never missing by truthiness.
3. **C08-03.** Add deterministic approved-vocabulary encoding and unknown policy. Research mode preserves typed lists; matrix mode requires supported scalar/encoding intent. Never learn vocabulary, scaling or imputation from the whole dataset.
4. **C08-04.** Link eligibility warnings to controls. Outcome/identifier/time roles are not predictors by default. Flag mechanical risks without claiming clinical validity or leakage-free training.

Finish line: J08 and literal X/y/ID separation. Migration applied twice yields the same state. Physical column keys remain stable across label edits.

## C09 — Full-population Check

Before: complete validation is tied to publication, and failure can stop early. After: a user starts Check, reloads while it runs, opens a real issue, edits and rechecks without replacing the active publication.

Ownership: lifecycle publish/check orchestration, existing candidate materialization/publication quality, Arango operation records and CheckPanel. This is not a second execution engine.

Checkpoints:

1. **C09-01, R5.** Specify and fault-test queued/running/complete-pass/complete-fail/incomplete-failure/canceled transitions, operation identity, leases, retention and cleanup. Split candidate execution from activation. Browser reload attaches to an operation. Worker restart restarts or resumes only at an executor-supported boundary.
2. **C09-02.** Add recoverable per-cell diagnostics to the same checked execution path. Accumulate counts and bounded example references. Fatal errors preserve partial scope/counts. Diagnostic placeholders cannot be activated as valid values.
3. **C09-03.** Display progress/completeness honestly; issue links open the appropriate feature/binding editor. Draft or dependency changes mark the report stale immediately. Retry with the same identity is one logical operation.
4. **C09-04.** Reuse complete candidates only under exact identity match and fresh authorization. Test cancellation versus activation, process crash, duplicate submit, stale generation, revoked scope and cleanup races. Keep old active data through every unsuccessful attempt.

Finish line: J09 with more than one bad record beyond preview bounds. Early failure may prove invalidity but cannot claim complete issue counts. This WP needs real database fault tests, not only mocked state transitions.

## C10 — Copy and reviewed refresh

Before: newer data/mappings risk changing meaning implicitly. After: the user copies a design independently or reviews and applies a refresh to a new generation.

Ownership: Explorer lifecycle/owner persistence, interpretation library and selection lifecycle; copy/refresh comparison UI. No cross-project sharing or approval platform in scope.

Checkpoints:

1. **C10-01.** Copy to a new owner identity with authorized pinned dependencies and independent mutable draft. Never share mutable columns between originals/copies.
2. **C10-02.** Resolve a new generation into a proposal with membership changes, missing concepts, changed value arms/units and mapping applicability. Freeze new membership only through explicit apply.
3. **C10-03.** Show old/new differences and unresolved decisions, cancel without changes, apply via CAS and require fresh Check. Do not auto-match by display name or adopt latest mapping heads.
4. **C10-04.** Use two generations with one added record and one changed measurement type. Verify original publication values and retained old downloads remain unchanged. Test unavailable retention and revoked access.

Finish line: J10; honest retention errors rather than rebuilding an old artifact from current data.

## C11 — Typed downloadable artifact

Before: a correct displayed table can lose types/missingness in a download. After: the user downloads a pinned research table or matrix bundle and the supplied Python loader reads exact intended values.

Ownership: lifecycle artifact, `dataframe/published/artifact.go`, existing streaming storage, Viewer export action and bundle loader/schema. Replace competing writers in this workflow; do not leave two meanings for Export.

Checkpoints:

1. **C11-01.** Show revision, population, scope and export mode. Default complete checked population. A filtered export explicitly freezes/records that scope; current Viewer filters cannot silently change the artifact.
2. **C11-02, R6.** Version schema/manifest for roles, physical keys, dependencies, missingness and encoding. Choose one lossless on-disk encoding after marker-collision/list/large-integer proof. Stream server-side; no full browser Blob assembly.
3. **C11-03.** Include a tested loader with pinned supported dependency versions and exact nullable types. Return research data or X/y/IDs. Document numeric tolerances where necessary; do not accept blanket stringification as type correctness.
4. **C11-04.** Consume a DOM-downloaded artifact with Python and compare against independent literal values. Test quoting, literal null markers, lists, timestamps, categories, false/zero, large integers, checksums, truncation, cancellation and authorization.

Finish line: J11. Same retained artifact bytes have the same checksum; a rebuild is not assumed byte-identical without canonical archive metadata. Loader proof is not proof of useful model accuracy.

## C12 — Integrated release evidence

Before: isolated features may work with hidden test setup. After: complete ordinary user journeys work against hostile fixtures and real CDA data, with measured limitations.

Ownership: existing dev-loop browser driver and verification scripts, fixture generation/manifests and operator docs. Do not create a parallel test platform.

Checkpoints:

1. **C12-01.** Assemble J01–J12. Setup may ingest raw resources/authenticate but cannot seed columns, routes, interpretations, collections or successful checks. DOM creates business state; read-only API/DB probes inspect it independently.
2. **C12-02.** Use `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META`, record manifest/license/generation and derive a deterministic CI subset. Inspect actual available concepts before choosing scientific task examples; do not invent a biomarker that is absent.
3. **C12-03.** Run two actual research tasks with different row types, including non-Patient. Use raw-source independent expected calculations for a small subset. Use synthetic fixtures for failure modes absent from CDA.
4. **C12-04.** Run the integrated suite once, measure search/preview/operation-ack and complete Check/export cost separately, then perform five warm edit-to-proof iterations. Publish evidence index with SHA, machine, dataset, results, unsupported cases and actual reachable local URL.

Finish line: all ledger functional/performance gates have evidence. Human task success remains unmeasured until a usability study; it does not block autonomous functional implementation or justify claiming nontechnical usability has been proven.

## Dependency and parallelism decisions

Default order remains C01 → C03 → C02 → C04 → C05 → C06 → C07 → C08 → C09 → C10 → C11 → C12. This is a sequencing choice, not a claim that every adjacent pair is technically dependent. C01 is the first product checkpoint; C03 enriches it before optional population work.

After C01, C02 source selection and C03 metadata import have largely separate backend owners, but share capability/context and Builder surfaces. After C03, C04 inspector and C06 mapping can progress separately only after catalog/reference contracts are fixed. C05 and C07 also share compiler/authoring contracts. None is automatically safe to fan out across the entire monorepo.

If parallel work is explicitly requested, assign disjoint backend-plus-test areas, give the integration owner sole control of shared wire types/Builder/ledger, and create branches only for those independent lines. Workers must not each redesign canonical feature identity. Avoid simultaneous edits to authoringv2, generated contracts and compiler semantics until a shared contract commit lands.

## Prototype evidence and limits

[Open the interactive selection study](catalog-prototype.html). It contains 1,000 synthetic concepts and two alternatives. Variant A keeps a cross-page basket and applies selections together. Variant B adds each selection immediately. Both expose construction; neither connects to Loom.

Browser probe on 2026-09-18 established: 50 visible entries per page; selection across page/search; canceling a required choice in A leaves zero columns and three selections; resolving it adds exactly three; construction inspector exposes the source route; 390px layout has no horizontal overflow; canceling the third choice in B leaves its two earlier additions; no browser exceptions in those actions. Desktop/mobile screenshots were visually inspected. The original `scripts/verify_ml_dataframer_prototype.mjs` probe is retained in Git history; its standalone launcher was retired during the native Playwright Test migration. Historical evidence is local under `.audit/ml-plan-detail-20260918/`.

Choose A because the user's explicit multi-selection is a single reviewable operation. B has lower friction for individual additions but requires undo/partial-result handling for the mixed-ready case. This is an observed interaction-state difference, not a human preference study. The prototype uses an in-memory synthetic list, so it does not prove server paging, reload persistence, API atomicity, FHIR recognition, accessibility compliance or performance KPIs.

Existing focused tests passed on the planning baseline:

```sh
go test ./internal/explorer/authoringv2 ./internal/fhir/schema \
  -run 'TestApplyCommands(RejectsBatchAtomically|AcceptsCorrelatedLookupWithoutLegacyPath|SourceEditsPreserveColumnIdentityAndAreIdempotent)|TestValidateCorrelatedBinding' -count=1
```

The local run used a writable temporary GOCACHE because the sandbox denied the default cache. These tests substantiate existing atomic-command and binding-validation seams, not the proposed semantic add command or no-column preview. No production implementation tests are claimed.
