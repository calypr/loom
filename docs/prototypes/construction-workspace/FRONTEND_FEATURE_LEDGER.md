# Dataframe builder feature ledger

This is the delivery ledger for the researcher-facing builder. Each row is one
interaction to ship and QA as a visible feature. Its dependent calls can be
built in the same slice. P01–P09 in
[WORK_PACKAGES.md](WORK_PACKAGES.md) remain the technical work map, but a package
is not a user-visible completion claim.

The builder's job is to frame sparse FHIR records as an ML-ready dataframe:
choose what one row represents, which related records qualify, how zero or
many matches appear, and which source values become columns. Arbitrary derived
calculations are outside this builder. The old Calculate control is excluded.
Combine is recorded below because code for it exists, but it is hidden and is
not a shipping claim. It only earns a place in the visible UI when combining
previously framed tables is necessary for a researcher task and the exact
versioned input can execute through the shared path.

Source state: `feature/construction-workspace` at `40607fe90`, 2026-09-25.
The entries below are the original inventory. The CDA checks on 2026-09-27
advance the following exact paths:

| Feature | DOM usability | CDA result correctness | Persistence | Measured preview |
| --- | --- | --- | --- | --- |
| 02, start a table | New table → BodyStructure creates a table with an automatic ID column and renders rows without entering a name, reselecting the table, or clicking Preview. | Visible BodyStructure IDs matched the raw CDA source. | Reload preserved rows; deleting the temporary table restored the prior table list. | 436 ms from choosing the row type to rendered rows; 232 ms after reload. |
| 02a, repeated row shape only | Configure rows offered distinct FHIR repeated paths and a preserve-parent policy; its proposal showed the new row count before Apply. Changing to a different root remains unverified. | One Observation with three component codings became three rows with values matching the exact source order. ALL and FIRST forms both passed. | Apply, reload, edit back to source-record rows, remove the temporary column and filter, and full restoration passed. | Row-change proposal 411 ms; expanded preview 271 ms. |
| 06, ready paired value column | Coded value suggestions show a distinct Add column button. It opens route and result-form choices, then a rendered proposal before Apply. | `days_to_collection` matched its raw CDA code/value component. | Apply, reload, and remove passed. | Proposed rows rendered in 452 ms on the 2026-09-27 rerun. |
| 06a, related route choice | Subject and Focus paths explain their relationship and show zero, one, and many matches for displayed Patient rows before selection. | Subject showed 38 matches and Focus 1 for the same Patient, matching the raw CDA oracle. | Route selection persisted through the related-column lifecycle. | Match counts rendered in 836 ms. |
| 07 and 07a, related Observation forms | COUNT, ALL, and PRESENCE choices are operable in one Add columns flow. | A Patient with 38 matches and one with zero produced the expected count, list length, and presence values. | Apply, reload, saved-step edit, removal, and cleanup passed. | Related proposal preview rendered in 707 ms. |
| 09, expand related records | The route chooser shows Subject and Focus meanings and defaults to retaining a parent with no match. Optional policies are under Advanced. | Two bounded Patients produced 38 matching Observation rows and one retained no-match row; changing the policy removed that row. | Apply, reload, edit, remove, and restoration passed. | Default proposal rendered in 631 ms. |
| 12, group rows | Group keys and count summary are available with a collapsed Advanced section. | Bounded BodyStructure rows grouped into one checked source-system category with count 135. | Apply, reload, edit label, remove, and restoration passed. | Group proposal rendered in 816 ms. |
| 13, expand repeated values | Repeated BodyStructure values are selectable in Reshape, with no-match behavior visible. | Expanded rows and values matched the raw CDA nested array oracle. | Apply, reload, edit, remove, and restoration passed. | Expansion proposal rendered in 657 ms. |
| 14, numeric Pivot | Category, value, group, duplicate-value policy, and accepted categories are operable. | Observation sums matched raw CDA source groups; a separate two-record publication matched its ClickHouse row. | Apply, reload, edit, remove, restoration, and bounded publication passed. | After removing the redundant source-row sort, full-CDA category discovery rendered in 4,083, 4,092, and 4,085 ms, below the five-second gate; the prior median was 5,140 ms. Proposal previews stayed under two seconds. |

Browser evidence: `.artifacts/cda-builder/2026-09-27T22-02-23.178Z/` and
`.artifacts/cda-builder/2026-09-27T22-07-07.266Z/`,
`.artifacts/cda-builder/2026-09-27T22-16-03.957Z/`,
`.artifacts/cda-builder/2026-09-27T22-17-14.432Z/`,
`.artifacts/cda-builder/2026-09-27T22-17-38.906Z/`, and the two numeric
Pivot runs at `2026-09-27T22-12-19.503Z` and `2026-09-27T22-13-37.987Z`,
and bounded ClickHouse publication at `2026-09-27T22-18-59.320Z`.
The full-source numeric Pivot performance reruns passed at
`2026-09-27T22-37-47.665Z`, `2026-09-27T22-38-37.910Z`, and
`2026-09-27T22-39-21.216Z`; each repeated the CDA value and lifecycle checks.
The ready paired-column flow passed again at
`.artifacts/cda-builder/2026-09-27T22-30-27.979Z/`: its rendered value
matched the raw CDA Observation, and Apply, reload, and removal succeeded.
After making Add column a distinct control, the same complete browser path
passed at `.artifacts/cda-builder/2026-09-27T23-09-17.643Z/`. The proposed
column rendered in 452 ms; the displayed `days_to_collection` value 366
matched the raw CDA Observation component. No browser request failed.
The unnamed initial table and repeated-row lifecycles passed at
`.artifacts/cda-builder/2026-09-27T22-48-32.535Z/`,
`.artifacts/cda-builder/2026-09-27T22-44-34.752Z/`, and
`.artifacts/cda-builder/2026-09-27T22-45-21.456Z/`.
These paths do not constitute full release acceptance. The current CDA Builder
is available at
`http://127.0.0.1:30008/?project=loom_dev_cda_fhir&explorer=cda-builder-full-qa-1790440983382&mode=builder`.
That Explorer is a fresh QA draft. Its builder API reports 159,047 Patient,
815,261 Observation, and 742,505 Specimen records. Those are authorized
source-record counts, not the row count of an authored table. The earlier
`30006` preview uses a small development fixture and must never be used to
judge CDA coverage. The old `loom-dev-bootstrap` draft in that small fixture
contains an invalid `unknown`-typed source column and currently fails
construction capability compilation.

## Status vocabulary

- **CDA ready** means the current browser interaction has completed against
  the loaded CDA generation, including its displayed result.
- **Fixture path** means a real browser/API path and result were checked on the
  small local fixture. It is not a claim about CDA scale or coverage.
- **Code only** means the editor or backend exists but the complete user path
  has not passed a live QA run.
- **Blocked** means the intended action is hidden, fails, or cannot execute
  through the current shared construction contract.
- **Missing** means the user decision has no usable frontend control or no
  backend capability that can supply its choices and execute it.

## Researcher feature inventory

The API paths below share `/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2` unless an absolute path is shown. `commands` always carries the current snapshot and draft revision. `construction-proposals` is the shared preview-before-Apply endpoint for staged operations.

| # | Feature and user result | Existing UI and backend/API calls | Current status and exact gap |
| --- | --- | --- | --- |
| 01 | **Choose the loaded dataset and open a table.** The researcher sees the active generation and which source counts are being shown. | Explorer picker; `GET /projects/{project}/explorers`, `GET /builder`; catalog nodes in the builder response. | **Code only on CDA.** The current CDA API returns real counts, but the UI does not identify the generation or distinguish source-record counts from table-row counts. The previous preview link pointed to the small fixture. |
| 02 | **Start a table from one row type.** One click on a populated record type creates a table with a useful identity column and displays rows. Naming is optional and editable afterward. | First-table form, `RowRootPicker`; `POST /commands` with `CREATE_TABLE` and `ADD_COLUMN`, then `POST /reconcile` and `POST /preview`. Backend catalog supplies eligible roots, counts, and field candidates. | **Blocked.** The current first-table path requires a name, then a row type, then a column, then Preview. It does not show rows immediately. A saved `unknown` source type can block compilation. This is the first implementation and QA slice. |
| 02a | **Change what an existing row represents.** Select a new root or related occurrence, inspect which columns and steps need repair, and retain the accepted frame until the replacement previews successfully. | Row-root/row-change controls; `POST /row-change`, `POST /construction-capabilities`, `POST /construction-proposals`, `POST /commands`. | **Code only.** Assessment and step repair pieces exist, but changing row meaning in the unified workspace has not passed a complete live path. |
| 03 | **Manage tables.** Select, rename, duplicate, reorder, or delete a table while preserving versioned inputs. | Left table navigation; `CREATE_TABLE`, `RENAME_TABLE`, `DUPLICATE_TABLE`, `REORDER_TABLES`, `DELETE_TABLE` commands. | **Code only.** Controls exist. New table still uses a browser prompt after the first table. Immutable input retention on delete has not passed the Combine path. |
| 04 | **Inspect the current frame.** See row meaning, columns, sample rows, and whether a preview is complete or sampled. | Workspace table and Preview; `POST /reconcile`, `POST /preview`, `GET /builder`. | **Fixture path.** Basic rows display. Initial source-table preview is manual. CDA action-to-render time and sample/completeness copy are not yet accepted. |
| 05 | **Add a direct scalar field.** Search fields on the selected row type, inspect meaning/examples, and add one column without graph terminology. | Add columns → source catalog; `POST /construction-choices`, `POST /semantic-inventory`, `POST /commands` with `APPLY_CONSTRUCTION_CHOICE` or `ADD_COLUMN`. | **Fixture path.** Direct fields work. The menu is dense and mixes fields, codes, concepts, and graph choices. Unsupported `unknown`/container fields need an explicit nonselectable state and saved-draft repair. |
| 05a | **Manage columns.** Rename, hide, reorder, and remove an output column; show which later steps use it before removal. | Column selection/actions and step commands; `POST /construction-capabilities`, `POST /construction-proposals`, `POST /commands`. | **Code only.** Column selection and some edit actions exist, but the complete dependency-aware remove/reorder path has not passed live QA. |
| 06 | **Add an observed code or code set.** Choose exact observed code identities, output form, and value handling. | Concept catalog and code chooser; `POST /semantic-inventory`, `POST /construction-choices`, `POST /commands`. | **Code only.** Search and individual choices exist. A researcher-selected set across pages, output-row coverage, precise system/version identity in the review, and saved set editing are not demonstrated end to end. |
| 06a | **Inspect a route when needed.** Show the path from current rows to a source, then let the researcher select among executable alternatives. | Advanced graph and source menus; `POST /construction-choices`, `POST /related-expand-choices`, `POST /population-routes`. | **Code only.** Route controls exist in different panels. The ordinary path should be a direct source choice; the graph must explain an ambiguity rather than make the researcher configure traversal first. |
| 07 | **Add related information to existing rows.** Choose a related source and make a value list, record count, or presence flag without changing row identity. | Add columns source picker, `RelatedSourceStepEditor`; `POST /construction-capabilities`, `POST /construction-choices`, `POST /construction-proposals`, then `APPLY_CONSTRUCTION_PROPOSAL`. | **Fixture path.** Patient → Observation list/count/presence, preview, reopen, publish, and trace passed on one route. CDA and a short default route choice remain unverified. |
| 07a | **Choose how zero or many matches appear.** State whether zero matches yield null, empty list, zero, false, or no row; for many matches choose list/count/presence, one ordered record, or expansion. | Related-source and related-expand policies; `POST /construction-capabilities`, `POST /related-expand-choices`, `POST /construction-proposals`. | **Code only across separate editors.** Some forms are implemented, but a single decision flow and evidence about how often each case occurs are missing. A scalar selection needs a deterministic ordering rule. |
| 08 | **Choose which related records contribute.** Filter related records by a supported field's presence or exact string/code value; later add time windows and other-field conditions. | Contributor controls in related-source and related-expand editors; `POST /related-expand-contributors` for expansion field choices, `POST /construction-proposals` for exact result. | **Fixture path for the scalar rule; incomplete feature.** A saved Specimen expansion with `id = dev-j02-specimen` now previews READY after a compiler fix. The expansion condition UI and CDA path are not yet QA-ready. Time windows, conditions on another/repeated field, and output-row coverage are missing. |
| 08a | **Place related records in time.** Choose an anchor date, interval, and before/after relationship from available fields; preview eligibility and missing-date treatment. | Contributor/relationship policy editors; capability and proposal endpoints. | **Missing as an approachable general flow.** The current field and contributor menus do not expose a complete metadata-driven temporal choice with denominator evidence. This must work across eligible FHIR resources, not a hardcoded Observation rule. |
| 09 | **Turn related records into rows.** Select a proven starting record and path, choose the no-match rule, and see one row per related record. | Reshape → `RelatedExpandEditor`; `POST /construction-capabilities`, `POST /related-expand-choices`, `POST /construction-proposals`. | **Fixture path.** Patient → Observation → Specimen preview, Apply, reopen, and ClickHouse publication passed. The path picker remains too technical; row multiplication and contributor evidence are incomplete. |
| 10 | **Add a field from the current related record.** Once rows represent a related record, add its scalar field to those exact rows. | Add columns → exact related record; `POST /related-field-choices`, `POST /construction-proposals`. | **Fixture path.** Observation status was added after expansion and published. No CDA QA, repeated fields, or generalized missing-value policy. |
| 11 | **Keep rows.** Filter by a typed column value or presence, or by a related-record condition, without changing source data. | Keep rows → `ConstructionOperationEditor`; `POST /construction-capabilities`, `POST /construction-proposals`. Population controls also use `GET /row-definition-choices`, `POST /row-definition-proposals`, and `POST /population-routes`. | **Code only.** Simple staged `FILTER` is authored. Related-record population decisions live in separate older controls; a researcher cannot yet discover and apply them as one short Keep rows flow. |
| 11a | **Define eligible rows.** Express existence, absence, or counts of related records, with the relationship and contributor criteria visible in the same decision. | Row-definition and population editors; `GET /row-definition-choices`, `POST /row-definition-proposals`, `POST /population-routes`, and the shared construction proposal path. | **Code only across two authoring paths.** The older population controls and staged Keep rows editor need one researcher-facing flow and one result preview. |
| 12 | **Group rows into a frame.** Select group keys, missing-key behavior, and count/reduction outputs; keep identities clear. | Reshape → `ConstructionReshapeEditor`; `POST /construction-capabilities`, `POST /construction-proposals`. | **Fixture path for narrow cases.** GROUP and missing-key policies passed an Arango oracle and reopen check. CDA usability and compatible quantity/code reductions are open. |
| 13 | **Expand repeated values.** Make one row per list item with an empty-list rule and optional position. | Reshape → `ConstructionReshapeEditor`; capabilities and construction proposals. | **Code only.** Typed `EXPAND` and guided controls exist. A live CDA list, paired repeated fields, and resulting row evidence have not passed. |
| 14 | **Turn categories into columns or columns into rows.** Control categories, missing/unlisted values, schema refresh, and output identities. | Reshape pivot/unpivot editors; `/table-shape-capabilities`, `/table-shape-category-discoveries`, `/table-shape-proposals` for older shapes; `/construction-capabilities` and `/construction-proposals` for staged shapes. | **Code only in the unified workspace.** Legacy shape paths work separately. Repeated staged composition, new-category acceptance, and CDA preview/reopen need one coherent path. |
| 15 | **Combine with another finished table.** Choose an exact published input, then match columns, append rows, or compare membership. | `ConstructionCombineEditor` and `/construction-inputs` exist, but the visible action bar hides Combine. `COMBINE` construction lowering is restricted and the current panel reports unavailable. | **Blocked.** Backend and editor fragments do not yet satisfy an intermediate AQL result joined to an immutable ClickHouse artifact, followed by another operation. Do not expose the control as a working feature until that path passes. |
| 16 | **Edit, remove, branch, and undo a step.** Reopen the saved decisions, preview downstream effects, and keep the accepted result until Apply. | Step history; `/construction-capabilities`, `/construction-proposals`, `APPLY_CONSTRUCTION_PROPOSAL`, `RESTORE_DRAFT_REVISION`. | **Fixture path for supported steps.** Related source/expand/field steps reopen; removal and stale preview are implemented. Repairing downstream references, branch-from-step, and every operation family are incomplete. |
| 17 | **Understand the dataframe.** For each output row/column, show missingness, multiplicity, coverage denominator, contributing records, and evidence limits. | Review/profile panels; `/population-mapping`, `/cell-trace`, `/column-source`, `/configured-column-context`, `/semantic-inventory`. | **Fixture path for one related source.** Sparse report and cell trace passed there. Generalized output-row coverage, sampled/unavailable labels, and a published data dictionary are missing. |
| 17a | **Review a source refresh.** Show changed row counts, codes, categories, and column schema before accepting a new generation. | Builder/catalog refresh, semantic inventory, category discovery, and reconcile/publish calls. | **Missing as one review flow.** Pivot categories have a discovery endpoint, but a general current-versus-new generation impact report and explicit acceptance for schema changes are not available. |
| 18 | **Save, publish, reopen, and export.** Published ClickHouse rows agree with the edited preview and carry a retrievable artifact. | `/reconcile`, `/publish`, `/artifacts`, artifact download, `GET /builder`, Viewer/GraphQL. | **Fixture path.** One related-record chain published and read back from ClickHouse. The same gate has not passed the CDA feature sequence, Combine, and all reshape forms. |

## Backend ownership and call sequence

| Frontend need | Endpoint family | Backend owner and responsibility |
| --- | --- | --- |
| Loaded data, roots, fields, and authorized counts | `GET /builder`, `POST /semantic-inventory` | `internal/server` request boundary; Explorer catalog/capability and lifecycle discovery. Counts describe the authorized source, never an authored frame. |
| Available action at the current step | `POST /construction-capabilities`, `POST /construction-choices`, `POST /related-expand-choices`, `POST /related-expand-contributors`, `POST /related-field-choices`, `POST /construction-inputs` | `internal/explorer/capability` and lifecycle choice generation derive executable options from the current typed stage, catalog snapshot, and authorization. The UI should render these choices directly rather than invent its own permissive menu. |
| More specific discovery, including older authoring paths | `GET /row-definition-choices`, `POST /population-routes`, `POST /table-shape-capabilities`, `POST /table-shape-category-discoveries` | Lifecycle discovers related-record and shape options. These calls currently feed separate panels; a unified interaction must preserve their compiler checks. |
| Preview a proposed edit | `POST /construction-proposals`; older `POST /row-definition-proposals` and `POST /table-shape-proposals`; `POST /preview` | Lifecycle validates policy, authorization, and revision, then uses the shared dataframe recipe/compiler/AQL path for rows. Proposal receipts tie the displayed preview to the exact edit. Preview speed is a product requirement; record action-to-render latency on real CDA data. |
| Persist, reopen, remove, undo | `POST /commands`, `POST /reconcile`, `GET /builder` | `internal/explorer/authoringv2` owns table and step state; lifecycle applies commands and validates revisions. Saved parameters and stable IDs must reconstruct the same editor state. |
| Explain the result | `POST /population-mapping`, `POST /cell-trace`, `POST /column-source`, `POST /configured-column-context` | Lifecycle evidence services identify contributing records and the scope of coverage. Missing denominators must be explicit rather than inferred from catalog counts. |
| Finish | `POST /publish`, `POST /artifacts`, `GET /artifacts/{id}`, Viewer/GraphQL | Lifecycle publication compiles the recipe and materializes it in ClickHouse; artifacts and reads must match the accepted preview. |

The shared execution path is typed operations in `internal/dataframe/recipe`,
lowering/rendering in `internal/compiler`, and Arango AQL evaluation. Publishing
materializes the accepted result in ClickHouse. The frontend is an editor for
that contract. It must never independently predict that an operation will
execute.

## Shared interaction contract

Every staged edit uses the same sequence: select a compiler-proved input stage;
ask the backend for choices valid for that stage and authorization scope;
submit typed parameters to `POST /construction-proposals`; show its matching
row preview; then Apply through `POST /commands`. A saved step reopens with its
exact source, policies, stable IDs, and input revision. An unsupported choice
is absent or clearly disabled before a proposal is sent. A proposal failure
keeps the accepted table visible and explains the decision that failed.

The short path should take one visible action to open an operation, one choice
for the common case, and an automatically refreshed preview. Advanced policies
belong inside the same editor as the choice they govern. Choice calls must be
fast enough to feel immediate and cancellable when the user changes source;
the row preview is the slower call and needs its own measured latency budget.
No new frontend action should ship solely because an endpoint exists.

Discovery facts have scopes. A catalog count is a source-record count. A
preview row count applies to the current table result. A field's source-wide
frequency is not output-row coverage. The UI must label each denominator and
show `unavailable` when the backend has no trustworthy count.

## One-feature delivery loop

1. Choose one numbered feature. Write its smallest researcher task and click
   target before coding. The click target counts actions from the visible table
   through a correct preview, including opening menus. It is a design target,
   not a claim about the current interface.
2. Check its CDA data, existing endpoint responses, and one representative
   sparse or repeated case. Add a backend capability only if those responses
   cannot express the decision or execute the result.
3. Build the visible interaction. Keep decisions in one panel, use sensible
   defaults, and defer advanced policies until the user asks for them.
4. Run one live browser path on CDA through preview, Apply, and reopen. For a
   published result, compare the ClickHouse rows. Record actual clicks and
   action-to-render time. Run only focused checks for the changed behavior.
5. Hand over the exact URL and a one-minute QA task. Log the user's friction
   and fix it before starting the next numbered feature.

Feature 02 is next. Its initial acceptance task is: choose **Patient** from
the loaded CDA row types, immediately see a Patient identity column and a
bounded preview, and reopen the table with the same row meaning. The count
shown beside Patient must be labeled as 159,047 authorized source records,
while the preview labels its own row count or sample separately. The target
is one row-type click from a blank Explorer to the visible preview, with table
naming optional. The invalid `unknown` source-column draft must be repairable
without losing other tables.
