# Deliver an ML dataframe workbench

Status: C01 implementation started 2026-09-19; no C package accepted yet. Revision 3: detailed code-first contracts and execution runbook, superseding the editor-first C proposal. Baseline: `arch/integration` at `3f71ece81ddf3ae6e008e67e08fc5eb57d5682bd`. Prepared 2026-09-18.

This plan continues B01-B08 with user-facing delivery packages C01-C12. It replaces their broad product-completion claims, not their implementation history. A passing backend test or a preconfigured Builder screenshot cannot close a C package.

The user is a bioinformatician who knows the study and the variables they want, but does not know FHIR paths, coding structures, or graph traversal syntax. They can make scientific choices. Loom must explain the data choices that require those judgments.

## What the finished product lets someone do

Choose rows and population. Search or browse concepts present in that dataset. Select the wanted concepts and add them as columns. Inspect or customize their construction when needed. Check and download the dataframe.

Automatic interpretation creates catalog entries, not dataframe columns. Opening a dataset with 1,000 recognized concepts creates zero user-selected columns. The catalog is not a list of every possible code, join, time window, and aggregate combination. It groups concepts and exposes available source variants on demand.

Neither Patient nor DocumentReference is mandatory. Rows can represent any supported resource type. File collections are optional population constraints. Ordinary uncoded fields remain selectable alongside coded concepts.

The default screen has two distinct areas:

```text
One row per: Specimen                 Population: All authorized / Change

Available concepts                  Selected columns: 0
Search name or code [...]           Select concepts, then Add selected.
Filter: type / code system / status

[ ] Study group   Study vocabulary
[ ] Measurement A   system A         [Add selected]
    Via donor · multiple values
[ ] Specimen collection date         Preview contains selected columns only.
    Direct field

Unresolved data [Open]              Check / Export
```

Rows in the catalog show name, code/system when present, source relationship, value shape, and count scope where measured. They do not expand into hundreds of construction forms. Selection persists across catalog search and paging. A known scalar with one supported source path can be added directly. Ambiguous routes or many-valued scalar outputs get a short decision prompt only for the affected selections.

Every selected column has an inspect-construction action. The focused relationship graph and worked source example are part of that inspector, not hidden behind a global expert-only screen. Full-dataset graph exploration remains another entry into the same catalog and canonical authoring model. Users are not required to construct graph paths.

The generated mockups in the conversation are exploratory illustrations, not the acceptance contract. In particular, the expanded per-feature editor is optional. It must not replace the simpler catalog-selection workflow.

## Delivery milestones

| Milestone | Packages | New task the user can complete | Acceptance result |
|---|---|---|---|
| M1: choose data from a catalog | C01, C03, C02 | Browse automatically recognized concepts, add only wanted columns, optionally constrain starting records, and download | A 1,000-concept catalog creates zero columns until requested; selecting three creates exactly those three |
| M2: control how selected columns are made | C04-C05 | Inspect/change joins, contributors, repeated-value handling, time windows, and units | A worked example explains inclusions and exclusions; editing one feature does not silently change the population |
| M3: recover unresolved data | C06-C07 | Define a missing pairing, reuse it from the catalog, and separately recode result categories | A previously unresolved entry becomes selectable without changing source records or automatically adding columns |
| M4: repeatable ML handoff | C08-C12 | Declare roles, check all rows, reuse definitions, and load a typed artifact | Complete DOM journeys and literal artifact values on hostile fixtures and CDA-FHIR |

C01 alone must already produce a selected-column dataframe using pairings the existing backend recognizes. C03 expands automatic interpretation using definitions, and C02 adds optional collection authoring. Do not wait for every vocabulary or profile to be supported before delivering the basic catalog.

## Revision from the earlier proposal

C01 now delivers catalog-first selection, not just a rearranged Builder. C03 owns standards-backed interpretation and catalog enrichment. C04 exposes construction on demand. C06 begins with unresolved source inventory, even when no feature can yet be created. C02 and C05/C07-C12 retain their substantive outcomes.

C identifiers remain stable for references, but the ledger records plan revision 3. No C package was implemented or accepted before this revision. Do not reuse earlier KPI results against revised targets.

The detailed pass found concrete prerequisites: the 512-identity semantic cap silently drops new identities; current capability IDs identify fields containing multiple concept variants; route IDs are allocated inside authoring commands; and interpretation preview requires an existing column. Revision 3 specifies the replacement inventory, concept/binding identities, server-resolved atomic add, and no-column preview instead of describing these as already reusable UI operations.

## Why more backend work is needed

The existing backend already has immutable selections, row-root changes, correlated extraction, reductions, temporal/unit policies, interpretation revisions, quality reports, cell traces, and server-built artifacts. Those are valuable foundations.

The current UI does not provide a complete starting-record browser, cross-resource semantic feature discovery, an ambiguous-binding editor, value recoding, ML feature roles, or a standalone full-population Check. Several existing controls work only after a developer creates state through an API. New backend work must serve the concrete interactions below rather than expand capabilities in isolation.

Start with the [per-WP implementation runbook](ml-dataframer/RUNBOOK.md) and [implementation contracts](ml-dataframer/CONTRACTS.md). They supply the state transitions, ownership, failure behavior, migration rules and readiness experiments behind the summary below. See also [architecture and source evidence](ml-dataframer/ARCHITECTURE.md), [acceptance protocol](ml-dataframer/ACCEPTANCE.md), and the [execution ledger](ml-dataframer/execution.json). All KPI thresholds below are proposed acceptance targets, not measured results.

The [interactive catalog prototype](ml-dataframer/catalog-prototype.html) compares cross-page basket selection with immediate addition. It is a synthetic interaction study, not the running product or a backend performance test.

## C01: Browse concepts and add only selected columns

Depends on: none. Milestone: M1. Size: large. Main risk: a catalog must not become a hidden wide-table generator or a capped list that omits data.

User outcome: open a project, choose rows, search available concepts, select three, add them together, preview, save, publish, and download. No construction editor is required for already supported unambiguous scalar cases.

Implementation units:

- C01-01. Add the reachable dataframe entry and catalog/selected-columns layout in `ui/apps/demo/src/main.tsx` and `features/ExplorerBuilder`. Reuse authorized resource inventory and the existing create/root commands. Keep one `authoringv2.Workspace` and save queue. Show an empty selected list until the user selects concepts. The row identifier may be system-supplied but must be separately labeled, not counted as a user-added feature.
- C01-02. Extend `internal/catalog`, its Arango adapter, and `internal/explorer/capability` with a paged, authorized concept projection using existing recognized pairings. Preserve system/code, owner scope, value binding, and available source routes. Separate concept identity from source variants and future output choices. Inspect the per-field `maxSemanticObservations = 512` bound in `internal/catalog/semantic.go`: use resumable discovery/persisted keyed inventory beyond bounded samples, not a larger in-memory cap. Raw-field inventory and incomplete discovery remain visible.
- C01-03. Replace occurrence-only selection with searchable names/codes and filters for resource type, code system, and mapping status. Add a selected-concepts basket across search/pages and an explicit Add selected action. Resolve candidate IDs against the current capability snapshot, then use existing typed source commands and an atomic CAS batch. Add only selected outputs. Show route/multiplicity decisions only for affected candidates; never silently use FIRST. Explicitly chosen REQUIRE_ONE remains checked at execution. Use existing preview/publication/artifact flow.
- C01-04. Prove a 1,000-concept fixture across the profiler's per-field bound, zero columns on open, cross-page selection, duplicate-add handling, stale catalog/CAS rejection, and reload/export. Keep ordinary uncoded fields discoverable. Preserve an accessible Explore relationships graph view backed by the same workspace; switching views must preserve existing configured routes/columns and advanced constructions that the catalog cannot edit. Add visible source summaries and a basic inspector now; C04 adds interactive construction. Never call a bounded sample the complete catalog.

KPIs:

- K-C01-a: opening the 1,000-concept fixture creates zero selected feature columns; choosing three concepts across search/pages creates exactly those three, with literal values after reload/export. Neither discovery nor adding one concept creates its unselected reduction variants.
- K-C01-b: all 1,000 expected concept identities, including those beyond 512 for one field, are discoverable. Catalog pages contain at most 50 entries and lazy-load examples. Thirty warm searches meet the 2-second p95 first-page target on the recorded fixture/machine.
- K-C01-c: the ready-scalar journey needs zero source-path entry, manual pairing, graph editing, or per-column construction forms. Specimen-first and observation-first journeys both pass without business-state API setup. Ambiguous candidates receive a decision prompt rather than a guessed result.

Verification: catalog/store paging and authorization tests, same-owner source tests, atomic batch/CAS tests, focused UI selection tests, and a live DOM search-select-add-download journey. Catalog paging must be server-side, not slicing an already downloaded giant list.

## C02: Find and freeze the records to include

Depends on: C01. Milestone: M1. Size: large. Main risk: selection must not depend on publishing a dataframe first.

User outcome: browse project files or another resource type, search/filter, select records across pages or all matching records, exclude some, and use that collection as the population. Understand why selected files can yield fewer specimen rows.

Implementation units:

- C02-01. Add an in-repo project record picker with available dataset labels, IDs as fallback, supported typed filters, cursor paging, selected-count feedback, and an explicit "all matching" action. Reuse the published reader for published sources. For raw ingested resources, add a narrow authorized source-browse contract using catalog-backed fields and the existing compiler/query machinery. Extend `SelectionSource` for that source if needed. Do not make users publish a temporary table or download every ID to the browser.
- C02-02. Use `lifecycle/selection.go` and the Arango selection store to freeze server-resolved membership. Add source identity, generation, scope, filter, and exclusion binding for the raw-source variant. Keep human-readable collection names on mutable collection metadata that references immutable revisions. Reuse the existing naming owner if present; do not put rename operations on immutable membership.
- C02-03. Replace the `?selection=` prerequisite with an actual selection action in the workbench and an exported host handoff. Rework `PopulationPanel.tsx` and `RowDefinitionPanel.tsx` to choose a supported target row type and explain available routes in ordinary terms. Respect edge direction in compiled traversal while allowing supported inverse traversal. Offer alternatives when routes differ in meaning; never choose shortest as a claim of scientific correctness. Add load-more for unmatched members.
- C02-04. Add exact cross-page, all-matching/exclusion, shared-target, unlinked-record, stale-generation, and unauthorized-record tests. Reload preserves the frozen collection. A changed filter produces a new revision, not an edit to existing membership.

KPIs:

- K-C02-a: files 001, 002, and 004 yield two mapped sources, one unmatched source, and exactly specimen 001. Excluding 002 keeps that specimen with one mapped source. The UI shows all three different counts.
- K-C02-b: selecting files 001, 002, and 003 then excluding 002 yields exactly 001 and 003 after reload and export. A generated multi-page fixture proves the same across page boundaries.
- K-C02-c: 100% of rejected cross-project, stale-scope, or incomplete selections remain unattached. A file-free specimen collection completes the same task.

Verification: selection persistence and direct-importer tests, live source browse/freeze/population probes, one DOM collection-to-artifact journey. Source browsing is part of this repository's deliverable. Integration into a separately maintained Gen3 host is a separate rollout, not a prerequisite hidden in this package.

## C03: Recognize more concepts from FHIR and terminology definitions

Depends on: C01. Milestone: M1. Size: large. Main risk: confusing a helpful terminology label with proof of a valid extraction binding.

User outcome: search useful labels and codes for data Loom recognizes automatically. Standard structures and supported defined extensions appear without asking the user to pair fields. Unknown structures remain listed with a reason.

Implementation units:

- C03-01. Add a version-pinned definition import path alongside existing schema-loading orchestration in `internal/ingest/source_schema.go`, with pure definition interpretation in `internal/fhir/schema`. Consume the applicable FHIR version, supported profile/extension StructureDefinitions, and configured terminology data. Extend existing schema/generation metadata with definition-set identity. Use locally imported, checksum-pinned packages/terminology snapshots first. An optional configured downloader has an explicit trust/host policy; never fetch arbitrary URLs found in records or send records to a terminology service. Document distribution/license requirements before bundling third-party definitions.
- C03-02. Enrich `internal/catalog/semantic.go` facts through a narrow resolver consumed by the catalog. Resolve names and applicable LOINC attributes from a pinned terminology snapshot, and resolve supported extension bindings from definitions plus observed structure. Feed existing correlated/extension binding types into compiler validation. Keep automatic schema recognition, definition-backed recognition, and approved human mappings as explicit evidence sources. Labels, synonyms, or equal code strings across systems never prove equivalence. Definitions do not override contradictory instance data. Unsupported profile constraints are reported, not ignored.
- C03-03. Make enrichment visible in the C01 catalog: searchable labels and observed codes, expandable definition provenance, value examples, and separate recognition/column-decision states. Provide an import/retry-definition action for missing metadata before offering manual pairing. Refresh recognition from retained observations without requiring raw-data reingestion; use a resumable source scan if the needed inventory is incomplete. Refresh catalog pages without modifying selected columns. Pin definition/mapping dependencies in selected intent and receipts so later terminology updates do not silently reinterpret a saved dataframe.
- C03-04. Add conformance fixtures for base Observation pairs, same-owner components, defined simple/nested extensions, unavailable definitions, mixed types, and conflicting meanings. Measure CDA-FHIR coverage by supported structural binding group and separately by observed occurrences. Declare the denominator, scope, and completeness. Produce an executable inventory of recognized, unresolved, conflicting, and unsupported groups; do not promise a percentage before measuring it.

KPIs:

- K-C03-a: all explicitly supported standard/definition-backed fixture pairings become catalog candidates with zero manual mapping. Unsupported or conflicting cases do not claim automatic recognition. A/shared and B/shared still yield 111 and 222 separately.
- K-C03-b: definition import resolves the known extension fixture without source mutation or new dataframe columns. Search by its imported label and exact code/URL finds the same candidate. Pinned definitions support replay without network access.
- K-C03-c: every observed binding group in the coverage fixture is accounted for as recognized, unresolved, conflicting, or unsupported. Counts reconcile with the independent inventory. Unknown noncoding fields remain visible. CDA coverage is recorded as a measured result, not inferred from this fixture.

Verification: pure definition-resolution tests, catalog/compiler integration, definition-identity invalidation, and a DOM import/search/select journey. Test incompatible FHIR versions, missing dependencies, untrusted URLs, and data/definition conflicts. A terminology lookup alone cannot turn an unsupported extraction into an Add-ready candidate.

## C04: Inspect and customize how selected columns are made

Depends on: C03. Milestone: M2. Size: large. Main risk: hiding meaningful joins or requiring every user to configure every column.

User outcome: add a recognized concept through the catalog, then optionally inspect the relationship path and see which records produce a cell. Change contributor rules or output handling when the research question requires it.

Implementation units:

- C04-01. Build a focused construction inspector from the selected column's canonical intent and receipt. Show the row type, named relationships, directions and multiplicities, matching codes, owning value location, and reduction. Provide a worked row with included/excluded records and reasons using the existing trace/lifecycle machinery. Use the same editor when entering through the graph. Derive the view from saved intent; do not persist a separate UI graph.
- C04-02. Turn source ambiguity and multiplicity into short add-time decisions. Let users choose a supported source route and reuse a policy across compatible selected concepts. Preserve a full editor for exceptions. Keep conditions on contributors separate from filters that change population. A misleading sample of one match cannot establish guaranteed uniqueness; validate the chosen rule at execution.
- C04-03. Expose supported count/exists/min/max/list/require-one and ordered choices with ordinary labels. Add numeric mean/sum only where absent, through the existing semantic/compiler path. Optionally add a second output such as count for a selected concept, but never generate all possible outputs. Apply explicit batch changes through existing column commands. Preserve independent stable column identities and show every affected column; do not introduce hidden mutable sharing or another feature-definition store.
- C04-04. Verify zero/one/multiple contributors, tied values, alternative joins, and empty-input semantics. Count has zero and exists has false for empty contributors; scalar measurements remain missing unless explicitly handled. Match actual trace contributors to output values. Make inspector closure return to the selected list without changing the draft.

KPIs:

- K-C04-a: for a literal fixture row, inspection explains nine related records, four code matches, and one result under the explicitly chosen ordered policy. Each stage exposes included/excluded examples. Changing the route produces the independently expected alternative value. C05 adds the user-authored time window and its two-contributor intermediate stage.
- K-C04-b: a ready candidate can still be added without opening this inspector. Adding count as a second explicit output creates only that output and leaves row membership unchanged.
- K-C04-c: every offered reduction matches the executed fixture oracle. No implicit FIRST or row multiplication occurs. Batch edits list affected columns and preserve their identities; unselected columns remain unchanged.

Verification: executed semantic/compiler tests, source trace tests, focused UI inspector/decision tests, and a DOM add-inspect-change-preview journey. Population changes require a separate explicit action.

## C05: Build time-aware measurements with explicit units

Depends on: C04. Milestone: M2. Size: large. Main risk: apparently reasonable defaults can introduce future information or mix incompatible measurements.

User outcome: author "latest measurement in the 30 days before specimen collection, expressed in centimeters" and understand what happens to a missing date, tied measurement, or incompatible unit.

Implementation units:

- C05-01. Build a date-window editor using actual available event fields and row anchors. It works for any supported row type. Expose before/after bounds and inclusivity, latest/earliest selection, precision restrictions, and tie handling. Keep the existing temporal contract where sufficient and extend that contract where the UI currently hardcodes upper bound zero or instant precision.
- C05-02. Expose the compiler's supported unit conversions as capability data, with canonical unit and dimensional compatibility. Replace the frontend's four hardcoded conversion presets. The backend retains the closed supported conversion registry. Do not promise arbitrary UCUM conversion or add an external terminology service as a hidden dependency.
- C05-03. Explain excluded future measurements, unknown dates, unresolved ties, original units, and conversion results in feature preview. Never normalize text or unknown units by guessing. Apply normalization before a numeric reduction and only across compatible dimensions.
- C05-04. Verify boundary dates, mixed precision, timezones, tied timestamps, affine conversions, absent anchors, and incompatible units. Add a second independently authored feature with a different window to prove that policies are per feature.

KPIs:

- K-C05-a: a fixture with collection day 2026-01-31 includes a measurement on 2026-01-01 and excludes one on 2026-02-01 for the explicitly inclusive 30-day lookback. All other boundaries have literal oracle cases.
- K-C05-b: known equivalent height measurements in supported units normalize to the same value within declared numeric tolerance. Unsupported units remain visible as unresolved, never coerced.
- K-C05-c: zero excluded future records contribute to the predictor. A missing anchor produces the chosen explicit issue state, not an unrestricted measurement search.

Verification: temporal/unit semantic tests, executed compiler cases, and one DOM feature with explanation and export. A time window is a recorded modeling decision, not a promise of clinical or causal correctness.

## C06: Turn unresolved source data into reusable catalog entries

Depends on: C03. Milestone: M3. Size: large. Main risk: unresolved data is invisible if discovery starts only from already recognized pairs.

User outcome: open Unresolved data, find an unfamiliar extension or other source structure, inspect actual records, load its definition or define the missing pairing, and then select that concept from the ordinary catalog.

Implementation units:

- C06-01. Build an unresolved-data inventory from raw field/structure observations, not only `conceptCandidates` or selected features. Group by source/profile, owner/extension ancestry, and unresolved reason. Include no-coding, missing-system, missing-definition, ambiguity, and unsupported-shape cases. Page structural groups and examples separately. Allow access before any dataframe column exists. The inventory includes direct uncoded fields without pretending they need codes.
- C06-02. Reuse C03 definition import first where a definition is missing. For genuinely undocumented pairings, add a source-structure selector to `BindingReview` that builds a checked binding from observed nodes even when no paired candidate exists yet. The user identifies the concept key or explicit extension identity, value location, repeated owner, and applicability. Compile through `internal/fhir/schema/correlated.go` and existing interpretation rules. No arbitrary AQL. Extend explicit missing-system matching where needed; a wildcard cannot stand for absence.
- C06-03. Preview the mapping on bounded matching and contrasting examples before saving an immutable interpretation revision. This dataset-level mapping preview must work without a pre-existing output column and use the same compiler/execution path. Publish the approved interpretation to the catalog, not automatically to a dataframe. Existing selected columns remain pinned until an explicit reviewed update. Support cancel/revert, and preserve raw records and unresolved out-of-scope structures.
- C06-04. Prove the entire unmapped-to-catalog-to-selected-column journey, including a structure absent from initial concept candidates. Resolve mapping conflicts visibly; never let a human rule or imported definition silently supersede an incompatible pinned meaning. Preserve nested ownership, source hashes, and scoped identity through preview/apply/reload.

KPIs:

- K-C06-a: a fixture with no recognizable initial pairing is visible in Unresolved data before any columns exist. A DOM-authored interpretation makes it searchable/addable through the same catalog; mapping approval alone creates zero dataframe columns.
- K-C06-b: same-owner/nested cases preserve A/shared = 111, B/shared = 222, and left-only/right-only. The missing-system value 333 requires an explicit scoped decision and is not silently assigned to A or B.
- K-C06-c: all raw-source hashes and out-of-scope values remain unchanged. Cancel/stale preview does not change interpretation state. Reuse needs no second manual mapping, and changing a mapping does not silently alter pinned columns.

Verification: unresolved inventory coverage, structural selector validation, no-column mapping preview, revision conflicts, and a DOM resolve-search-select-export journey. An unsupported extractor remains visible as unsupported, not a task a human is expected to fix by guessing.

## C07: Recode values without confusing that with FHIR repair

Depends on: C06. Milestone: M3. Size: large. Main risk: mapping labels can silently discard rare or unknown values.

User outcome: map study values such as `case`, `CASE`, and `control` to declared categories, inspect the affected rows, and decide what to do with values not in the mapping.

Implementation units:

- C07-01. Add a closed typed value-recoding variant to the existing feature definition and reusable interpretation revision. Keep it separate from source binding and unit normalization. Support exact typed matches and explicit output categories first. Preserve the sequence: identify source, select contributors, extract/reduce, then recode. Unsupported combinations fail semantic validation.
- C07-02. Add a category mapping editor showing observed distinct values with counts and count scope. Allow explicit case variants rather than hidden normalization. Offer unknown policies: emit an explicit unmapped category with a typed raw-value companion, emit missing with a reason, or block the checked output. A category column cannot mix numeric originals with string categories. Excluding population rows is a separate visible operation, never a recoding side effect.
- C07-03. Lower recoding through the existing semantic/IR/compiler path and include policy/version in canonical intent, receipts, quality, and trace. Reuse interpretation preview/apply and revision persistence. Do not introduce a regex/formula language or an independent transform service.
- C07-04. Verify typed equality, overlapping mappings, null/empty-string distinction, unknown values, preview cancellation, and pinned revision behavior. Record original value, applied rule, and output value in the explanation.

KPIs:

- K-C07-a: fixture values `case`, `CASE`, `control`, empty string, null, and `unexpected` match the explicit expected mapping exactly. Unconfigured values never disappear silently.
- K-C07-b: recoding changes zero population memberships and zero raw source values. Reusing the pinned mapping reproduces the same categories.
- K-C07-c: before/after counts reconcile to the stated population or labeled sample, including unmapped and missing values.

Verification: transform semantic/compiler tests, executed numeric/string/category boundaries, and a DOM map-preview-apply-revert-export journey.

## C08: Declare the dataframe's ML contract

Depends on: C04 and C07. Milestone: M4. Size: large. Main risk: a scalar table is not automatically a scientifically valid training set.

User outcome: mark identifiers, predictors, outcomes, time anchors, and excluded columns. Choose missing-value behavior and understand which columns can go into a conventional numeric matrix versus a richer research dataframe.

Implementation units:

- C08-01. Extend `authoringv2.Column` with typed feature intent and output representation. Carry it through canonicalization, migrations, compilation receipts, public descriptors, and artifact schema. Keep stable authored keys when one feature emits several physical columns. Migrate existing columns to "role not yet declared", not automatically to predictor.
- C08-02. Add role and missingness controls to `FeatureEditor`. Keep absent, recorded null, invalid type, ambiguous value, and incompatible unit as distinct evidence. Offer keep-missing and missing-indicator outputs first. Numeric replacement requires an explicit constant; do not infer that unrecorded means negative. Identifier/outcome/time columns remain available in the exported bundle but separate from predictor matrix X.
- C08-03. Add bounded explicit category encoding for matrix output using user-approved vocabulary and deterministic physical columns. Unknown-category policy is required. Typed lists remain supported in research output; matrix output requires an explicit reduction or supported encoding. Learned imputation, automatic vocabulary fitting, scaling, and model fitting remain downstream to avoid training/test leakage.
- C08-04. Add eligibility checks and warnings for undeclared roles, unsupported matrix shapes, possible outcome-derived predictors, and missing time intent. Link each warning to the relevant editor. Distinguish mechanical validation from user scientific approval. Do not label a dataset clinically valid or leakage-free.

KPIs:

- K-C08-a: the reference artifact's X includes exactly declared predictors; y and identifiers are separate. No outcome or identifier enters X by default.
- K-C08-b: missing, false, zero, and empty string retain distinct intended meanings. Missing indicators and explicit category columns match the oracle row for row.
- K-C08-c: matrix export rejects unresolved list shapes and undeclared unknown-category policies with a repair link. Research export can preserve those values with honest schema/status metadata.

Verification: migration and emitted-column identity tests, semantic/compiler and artifact-schema tests, and DOM role/representation authoring. The checked matrix contract is deterministic data preparation, not a training pipeline that learns from the full dataset.

## C09: Check the complete population and repair problems in context

Depends on: C05, C06, C07, and C08. Milestone: M4. Size: large. Main risk: creating a second execution engine or claiming a preview sample proves full quality.

User outcome: click "Check dataset", leave and return while it runs, see complete row and feature counts, open an issue, fix the feature, and rerun. The previously published dataset stays active until the new one passes the required policies and is published.

Implementation units:

- C09-01. Add an explicit check lifecycle over the existing compiled receipt and publication candidate materialization. Separate candidate execution/quality from activation in `lifecycle/publish.go`. Reuse `dataframe/publication` execution and quality accumulation. Persist a bounded resumable operation state and exact receipt/execution identity. Do not build a generic job platform or run a separate quality-only extraction evaluator.
- C09-02. Expand per-feature quality evidence to include issue counts and bounded example cursors. The current accumulator can stop on the first semantic error; a definite failure is not a complete population scan. Add closed per-cell diagnostic results to the same checked execution path for recoverable data issues, so Check can count them without a separate extractor. Irrecoverable errors retain partial counts and cannot claim completeness. Never activate diagnostic placeholders as valid values. Add draft-bound preview cell explanations using the same trace semantics; keep published cell traces bound to the immutable publication.
- C09-03. Build `CheckPanel` and an issue detail view with "Edit this feature", "Review binding", and source examples. Use the queued/running/complete-pass/complete-fail/incomplete-failure/canceled operation union in CONTRACTS §8; idle and stale are local or derived UI conditions. Disable claims for a stale check immediately after edits, membership changes, interpretation changes, or scope changes. Cancellation/retry must not activate a candidate.
- C09-04. Reuse a successful checked candidate on publish only when receipt, generation, scope, policy, and execution match. Reauthorize at activation. Test process restart, double submit, cancellation, cleanup of abandoned candidates, and retention of the old active revision.

KPIs:

- K-C09-a: completed diagnostic scans match independent full-population row/issue counts, including multiple bad records beyond the preview limit. Fatal early termination shows partial counts, not a full-population total.
- K-C09-b: every blocking issue class has an actionable editor/source link. Interrupted or bounded runs never display "Passed" or a full-population coverage percentage.
- K-C09-c: all tested draft/generation/scope changes invalidate check reuse. Failed/canceled/stale checks activate zero publications. Duplicate submit yields one logical operation.

Verification: publication/lifecycle fault tests and real database checks, then a DOM check-fix-recheck-publish journey. This is the largest new lifecycle change. Require a short written transition/retention contract before code, followed by its fault tests.

## C10: Reuse a dataframe without silently changing its meaning

Depends on: C09. Milestone: M4. Size: medium. Main risk: treating an updated dataset or interpretation head as an equivalent revision.

User outcome: copy a dataframe for another comparison, reuse approved features, and rebuild against a newer dataset generation after reviewing what changed.

Implementation units:

- C10-01. Add save/copy/library actions to the existing Explorer owner and interpretation library. Keep column/feature definitions together with their pinned dependencies. A copied dataframe gets a new owner identity; immutable receipts and mappings remain pinned references where authorized.
- C10-02. Add an explicit generation-refresh operation. Re-resolve source candidates, population rules, and interpretation applicability against the new generation. Freeze new membership. Present missing concepts, changed shapes/units, changed membership, and mapping applicability before apply. Reject unsupported rebinding instead of doing best-effort path matching.
- C10-03. Add a comparison UI with old/new population counts and feature decisions. Allow canceled refresh with no changes, and apply using existing CAS. Recheck before publish. Keep the old artifact download accessible under its original retained identity.
- C10-04. Verify clone independence, scope enforcement, exact-pinned replay, schema drift, unavailable retained inputs, and interrupted refresh. Use a deterministic two-generation fixture.

KPIs:

- K-C10-a: a copied definition reproduces the same rows and feature meanings on the same generation; editing the copy changes zero values in the original publication.
- K-C10-b: an added record and a changed measurement type are both surfaced in the refresh fixture. No new generation or mapping head is adopted without the explicit apply action.
- K-C10-c: when inputs are retained, an old publication still downloads the original artifact after refresh. When retention has expired, the UI reports that limitation instead of substituting current data.

Verification: lifecycle copy/refresh and permission tests, then a DOM copy-refresh-review-recheck journey. Cross-project feature-library sharing and collaborative approval roles are outside this release.

## C11: Download a dataframe that loads correctly in Python

Depends on: C09 and C10. Milestone: M4. Size: medium. Main risk: type or missingness loss during export despite correct displayed values.

User outcome: download either a typed research dataframe or a declared matrix bundle, see its exact scope, and use a supplied loader to obtain the intended columns without writing FHIR extraction code.

Implementation units:

- C11-01. Replace competing browser-built CSV and server artifact paths in the ML workflow with the existing pinned artifact lifecycle. Show published revision, row count, feature count, export mode, and whether viewer filters affect the request. Default to the complete checked population. A filtered export requires an explicit action and recorded frozen scope, not an invisible current-view filter.
- C11-02. Extend `dataframe/published/artifact.go` schema/manifest with C08 roles, physical-column mapping, interpretation revisions, quality, membership identity, missingness encoding, and a versioned loading contract. Reuse the streaming encoder and artifact store. Avoid browser Blob assembly for large datasets.
- C11-03. Supply a tested Python loading example or loader inside the bundle with declared dependencies and exact nullable types. Handle CSV quoting, literal null-marker collisions, booleans, large integers, dates, lists, and categories. If the existing CSV representation cannot distinguish a value from its missing marker, change/version the encoding rather than documenting data loss. Return X, optional y, and row identifiers for matrix mode.
- C11-04. Verify exported bytes and schema against actual typed values, not UI cell strings. Verify manifest checksums, old pinned downloads, cancellation/limits, truncated archives, and authorization. Migrate legacy export callers in scope rather than leaving two user-visible meanings for "Export".

KPIs:

- K-C11-a: the supplied Python loader reads every hostile fixture value with the declared type and exact value or documented numeric tolerance. Row order is canonical where required; comparisons otherwise use stable row identity.
- K-C11-b: preview, checked publication, Viewer, and loaded dataframe agree on selected row values and feature identities. The artifact's complete counts match the full check, not the preview size.
- K-C11-c: repeat downloads of the same retained artifact produce identical checksums. Changed scope or policy yields distinct recorded identity. Unauthorized download returns no artifact bytes.

Verification: existing artifact unit/fault tests plus loader integration tests and a DOM download consumed by Python. Model fitting is not required to prove type correctness. A small fixed-model smoke test may exercise the loader, but is not a claim that these data train a useful model.

## C12: Prove the complete workflow on hostile and real study data

Depends on: C11 and all earlier packages. Milestone: M4. Size: medium. Main risk: repeating the previous mistake of treating seeded state as user capability.

User outcome: complete the supported research tasks through ordinary project entry points on real data, with understandable failures and a responsive workbench.

Implementation units:

- C12-01. Assemble all [release journeys](ml-dataframer/ACCEPTANCE.md) into the in-repo browser driver. Setup may ingest raw FHIR and establish authentication. It may not create selections, graph routes, columns, interpretations, or completed checks on behalf of the user. Read-only API probes independently inspect what DOM actions produced.
- C12-02. Run the open-access `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META` dataset through the existing local ingestion workflow. Record content manifest/generation, machine, image/source SHA, and study concepts actually present. Add a CI-friendly deterministic subset with license/source provenance or a documented reproducible subset builder. Keep sensitive data out of screenshots and committed artifacts; open access does not make every future dataset safe to publish.
- C12-03. Prove at least two real-data tasks with different available row types, including a non-Patient start. Include at least one related feature, scoped collection, complete Check, typed artifact, and reload. Use hostile fixtures for error/repair cases absent from CDA. For a small real subset, compute expected rows directly from source using a test-only independent reference calculation, not Loom's compiler.
- C12-04. Measure the performance and usability targets in the acceptance protocol. Run the full integrated suite once after the combined feature work. Produce a release evidence index and a short operator guide with exact local commands and reachable URLs. Report unsupported cases and any unmeasured human-usability claims.

KPIs:

- K-C12-a: all release journeys pass without hidden authoring setup, with source SHA, expected/actual results, and artifact evidence.
- K-C12-b: the focused warm edit-to-DOM/backend-assertion loop has median at most 30 seconds across five repetitions. The full release run is measured separately and is not required to fit 30 seconds.
- K-C12-c: the performance budgets in the acceptance protocol pass on the recorded machine, or remain explicit release blockers. Real-data full-check time and peak memory are measured, not inferred from a two-row fixture.
- K-C12-d: automated task completion is reported separately from human usability. The suggested study target is four of five representative bioinformaticians completing the core task in 15 minutes without FHIR assistance. Until such a study happens, label it unmeasured; it is not a reason to block autonomous functional implementation.

## Execution order and parallel work

Use one integration line. Default order is C01, C03, C02, C04, C05, C06, C07, C08, C09, C10, C11, C12. Close each package with its own user-visible result before broadening scope. Do not implement all backend tasks first and all frontend tasks afterward.

C02 collection work and C03 definition enrichment are logically independent after C01. Freeze their shared catalog/capability contracts before assigning disjoint implementation ownership. C05 and C06 are also logically independent after C04, but both touch shared authoring/compiler contracts. Parallel work is safe only after the integration owner freezes those contract changes. Separate workers may then own the time/unit editor and the binding editor, with their disjoint tests. The integration owner alone edits shared schemas, generated bindings, canonicalization, and fixtures. If ownership overlaps, run serially.

C12's journey driver and real-data oracle can be developed incrementally after C02 in a separate test-only worktree. Its final acceptance depends on C11. Do not invent extra branches for sequential packages. Follow the current agent/model budget, with a bounded Luna worker when useful and one foreground final review per coherent package.

Within a package, implement one vertical example first, then hostile variants. Freeze the typed contract before concurrent frontend/backend edits. Record literal acceptance expectations before coding. Do not run the entire repository suite after every component edit; use the risk-based gates in the acceptance protocol.

## Scope limits and decisions

This release includes source/row choice, related features, explicit reduction, temporal/unit policy, human interpretation, exact value recoding, feature roles, bounded deterministic encoding, complete checking, reuse, and typed export. It does not include model training, inferred clinical truth, a general formula language, automatic joins across unrelated datasets, learned preprocessing, arbitrary terminology equivalence inference, or clinical decision support. Deterministic resolution from supported, version-pinned FHIR/terminology definitions is explicitly in scope under C03.

Arbitrary composite/event-level row expansion is not included. Each row initially represents one supported resource instance. Multiple input files can map to that row. A future visit/event grain must have an explicit identity and separate scope; it cannot be approximated by an accidental join explosion.

The unresolved implementation risks are complete paged concept discovery beyond bounded profiling samples, supported definition/profile resolution, raw-source all-matching selection, no-column mapping of structures without candidate pairs, full-check lifecycle/retention, and exact matrix/export typing. Each belongs to a named package above with a discriminating fixture. If an existing mechanism cannot support one, redesign that mechanism within its owner and update the package estimate before implementation. Do not silently narrow the user outcome.

Track progress by accepted packages and passing user tasks, not lines changed or backend completion percentage. All packages start as planned. A package is accepted only when its frontend, backend, failure cases, and literal artifact evidence agree.
