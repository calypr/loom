# Implementation contracts

Proposed revision 3 of the [delivery plan](../ML_DATAFRAMER_DELIVERY_PLAN.md). These are implementation decisions, not claims that the endpoints or types already exist. [RUNBOOK.md](RUNBOOK.md) assigns their implementation and tests. Existing source owners are in [ARCHITECTURE.md](ARCHITECTURE.md).

## 1. Separate four identities

| Identity | Meaning | Changes when |
|---|---|---|
| Concept key | An observed coding system, code, and explicit coding version; or a structural field/extension identity where no code exists | The source identity changes, not its display label |
| Binding variant | Where and how that concept owns its value, including resource/profile, repeated owner, extension ancestry, type arm, and recognition dependency | Extraction meaning changes |
| Selected feature | A stable authored column identity with a binding, route, contributor rules, reduction, time/unit policy, and pinned dependencies | User explicitly replaces/adds a feature; ordinary edits preserve its identity |
| Physical output | A stable output key derived from the feature and an explicit representation slot | The representation changes; labels alone do not rename physical keys |

Same code in two systems is never one concept. Missing system is an explicit absence state, not an empty wildcard. Unknown coding version stays unknown; it does not silently adopt the imported terminology version. Two structures carrying the same system/code may group under one search result but remain distinct binding variants. Do not merge them into an extraction until the user chooses a variant or an explicitly supported combination.

An extension identity includes canonical URL and the relevant ancestry/binding context, not its last URL segment or display text. Ordinary fields use resource type and normalized structural path; do not manufacture a coding system for them. Hash canonical structured identity, not concatenated ambiguous strings. Keep the readable constituent fields for explanations.

Selected features pin definition and interpretation revisions. The catalog can show a newer label or mapping without rewriting saved meaning. A source-generation identity is separate from a terminology revision. A receipt records both.

## 2. Inventory completeness is a storage contract

C01 must not use the bounded `SemanticObservation` list as a complete inventory. Keep bounded values/examples for display; separately maintain keyed observed binding groups and a resumable scan watermark. The initial migration rebuilds this derived inventory from retained raw resources when profiling has already dropped observations. Raising 512 to a bigger number does not satisfy this contract.

The inventory is a read model owned by catalog persistence. It is not another authored feature registry. Its exact Arango collection/index migration belongs in C01-02 after the storage readiness check. Required lookup dimensions are project, source generation, authorization resource path, resource type, structural binding identity, and normalized concept identity. Searchable labels are derived enrichment and cannot define uniqueness.

Choose a normalized durable inventory for steady-state paging, with the same grouping rules used by a resumable source scanner for old-generation backfill/recovery. A scan-per-search alternative avoids a new collection but cannot provide stable deduplicated paging cheaply without effectively rebuilding that index. Do not persist source payload copies: retain bounded examples/references and aggregate facts. Current overflow does not propagate reliable completeness through field documents, so an old inventory without the new coverage marker starts as unknown, not complete.

Required scan states are not-started, running, complete, failed, and invalidated. Store scanned resource counts, generation, cursor/checkpoint, and bounded diagnostics. A complete marker is committed only after the final resource range is processed for that generation. Restart may replay a range, so group upserts and count accumulation must be idempotent. Do not increment occurrence totals twice on retry. Choose per-resource contribution replacement or a rebuild-then-activate generation; document that choice before writing the accumulator.

For existing generations prefer a new versioned inventory build followed by pointer activation. The old complete inventory remains readable until replacement completes. An incomplete first build can be browsed with an explicit incomplete banner, never a claim that all concepts are present. If retained source is unavailable, say which inventory cannot be reconstructed; do not invent completeness from samples.

Authorization applies before examples, counts, facets, or result existence are returned. A project-wide aggregate is unsafe where a user's authorized population is narrower. Start with the existing authorization boundary, then prove whether it covers the whole generation or a subset. Subset discovery requires scope-bound projection or authorized source evaluation; hiding the raw example alone is insufficient. Cache keys include the effective authorization scope fingerprint and recognition revision. Reauthorize every request; a cursor is not a bearer permission.

## 3. Catalog reads and output readiness

Extend the existing authoring V2 transport family rather than adding a separate workbench API. Proposed operations below are names for contract design; finalize route spelling with OpenAPI in C01. Generate/update the repository's canonical client types rather than maintaining a handwritten competing schema.

| Operation | Inputs | Result | Forbidden effect |
|---|---|---|---|
| Browse concepts | Project/explorer context, row root, optional frozen population reference, query, supported filters, cursor, limit | At most 50 entries, opaque next cursor, inventory status and context token | Persisting columns or routes |
| Inspect concept | Context token, concept and binding reference | Supported source variants, bounded route alternatives, types, evidence provenance, lazy examples | Selecting a shortest route as scientific intent |
| Resolve selected | Context token, draft version/digest, selected references and explicit choices | Ready typed intent or per-selection decisions/stale/unsupported diagnostics | Partial draft mutation |
| Apply selected | Same guarded context, command ID, explicit resolved selections | Atomic workspace response plus selected-reference to stable-column mapping | Silently skipping rejected selections |

The context token binds project, source generation, authorized scope, inventory revision, recognition revision, and row/population context. Validate opaque cursors against the complete query/filter/context digest; changing a query starts a new cursor. Use a stable tie-breaker such as concept key for paging. Labels can change ordering only with a changed recognition/context revision.

C01 defaults to all currently authorized resources of the selected row type in the pinned generation. No selection revision, file collection or preseeded dataframe is required. The absent population reference is a typed all-authorized mode, still guarded by generation/root/scope identity; it is not an unrestricted query. C02 adds the alternative frozen-selection mode. A genuinely empty authorized source produces an empty catalog/table state, not a hidden fallback to Patient or project-wide data.

Catalog entry state has separate closed dimensions:

- Recognition: schema-recognized, definition-recognized, human-mapped, unresolved, conflicting, unsupported.
- Output readiness: ready, route-choice-needed, value-policy-needed, incompatible-with-current-rows.
- Count evidence: exact with denominator, sampled with sample scope, or not-computed. Counts are not probabilities of correctness.

Catalog availability means observed in the authorized source scope. It does not imply that every selected population row has a value. Population-specific fill rate must have separately labeled evidence. Do not compute full-population per-concept statistics synchronously on every search.

Route enumeration uses the existing supported route policy and bounded traversal. Start from the selected row resource, preserve edge direction and relationship identity, and page/bound alternatives. No Cartesian enumeration of all routes, reductions, time windows, and units. An unambiguous path is not proof of scalar cardinality. Add-ready scalars require structurally supported scalar semantics; sample uniqueness alone cannot establish that. Otherwise request list/reduction/checked-single-value intent.

Keep structural capability proof distinct from paged concept evidence. Existing capability candidates identify resource field paths and can bundle many concept variants. Do not append 1,000 distinct concepts as 1,000 structural field candidates or transmit the full inventory in Builder state. Resolve selected concept/binding references on the server and hydrate the dependency closure for the entire authored document plus the requested additions. Existing typed correlated sources already have structural validators independent of a field candidate ID. The structural capability snapshot and selected evidence share the same guarded generation/scope; neither a single UI page nor a client-submitted binding is sufficient authority. R1 must prove this path does not reload the entire concept inventory on every add/reconcile.

## 4. Add selected is one semantic command

Reuse `authoringv2.Workspace`, lifecycle authorization, compile validation, and the existing CAS store. Add a narrow semantic command for selected catalog references if needed; do not create another persisted design model.

Why the extra command resolution is necessary: `ADD_ROUTE` currently allocates occurrence IDs using command ID and index. `ADD_COLUMN_SOURCE` needs an occurrence reference. A browser must not reproduce that private ID algorithm or save routes first and columns second. The server resolves the selected references, allocates/reuses route occurrences within the cloned workspace, constructs checked column sources, validates the entire result, and commits once. Keep this deterministic transformation in authoring; database-backed reference resolution remains in lifecycle.

The read-only resolution operation uses the same resolution logic as apply. It returns choices, not an independently editable recipe. Apply repeats validation against the guarded context; a prior preview is not authorization to bypass current checks. Do not persist a generic workflow/plan object for this two-step interaction.

The basket retains selection intent across search/pages and canceled dialogs. It is UI state, not a second saved draft. After a reload, committed columns remain; do not promise uncommitted basket persistence unless scoped session restoration is implemented and tested. Root/population changes invalidate basket resolution and require an explicit review, not silent rebinding.

One command ID identifies one attempted mutation. Retrying an uncertain network result resends the identical body and ID, or queries the recorded result; it must not generate a fresh ID. Reusing the ID with a different body is rejected. Separate requests for the same canonical selected feature return the existing column as already-added, not a second hidden copy. A different reduction or route is a different feature intent, and an intentional duplicate is an explicit duplicate action with a new stable column ID.

Compute semantic duplicate identity from executable intent and pinned meaning, excluding the display label and assigned column ID. Do not treat all columns with the same code as duplicates. Retain existing authored column IDs on policy edits. Return added/already-present results for every requested item only after the atomic transaction succeeds.

UI states are browsing → resolving → awaiting choices → applying → saved, with canceled, stale, and failed transitions. Cancel before apply leaves all selected items in the basket and zero draft changes. CAS/context failure leaves the basket intact, reloads the draft, and offers re-resolution. Do not auto-replay a scientific choice against changed data. A network timeout after apply is an unknown outcome until command-result recovery, not proof of failure.

Initial bounds are page size 50, at most 100 selections per apply request, lazy bounded examples, and existing route-policy bounds. More than 100 selections requires an explicit smaller batch; do not quietly split a promised atomic request. These are proposed operational bounds to verify under C01, not measured performance results.

## 5. Definition import has a finite support boundary

C03 is a local, versioned metadata importer and supported resolver, not a general FHIR validator or terminology server. Existing `source_schema.go` snapshots graph-schema metadata; it does not already import profile packages. Keep I/O orchestration in ingest and pure structure interpretation in fhir/schema. Catalog owns resolved facts; lifecycle owns permission and activation.

Import lifecycle: stage local package/snapshot → validate size, dependency closure, declared FHIR version, canonical identity and digest → resolve supported facts → report unsupported/conflicting definitions → explicitly activate a definition-set revision. Failed imports leave the active revision intact. Imports never follow arbitrary URLs from instance data. Authorized administrators can install packages; ordinary users can request/retry recognition against installed sets without gaining server filesystem access.

Initial supported subset must be test-listed:

| Input | Automatic result | Stop condition |
|---|---|---|
| Base Observation code/value and same-owner component pairs | Existing checked correlated bindings, enriched names | Mixed or unsupported value shape; ownership conflict |
| Known terminology system/code/version in a locally supplied snapshot | Label, synonyms and available attributes, each with provenance | Missing version/term, incompatible system, unavailable licensed snapshot |
| Simple extension definition with one supported typed value arm | URL-scoped extension binding | Contradictory observed arms, unresolved modifier meaning |
| Nested extension definition with explicit child URLs and supported ancestry | Checked ancestry/value binding, if representable by existing compiler | Unsupported slicing/discriminator or unresolved owner correlation |
| Differential/profile dependency not yet flattened by supported import | No guessed extraction | Mark unsupported/missing dependency until explicitly implemented |

Do not promise all StructureDefinitions or arbitrary terminology equivalence. Implement a small documented subset and expand with fixtures. A definition supplies meaning/structure; instance units and actual type still govern extraction. Terminology labels alone cannot turn an unsupported binding into a usable column. Do not bundle third-party terminology until redistribution requirements are checked. The core journey must work with synthetic/open fixtures and an optional user-supplied licensed snapshot.

Recognition precedence is not “human always wins” or “standard always wins.” Identical compatible assertions can accumulate provenance. Incompatible bindings become conflicts requiring an explicit scoped choice; existing pinned columns retain their revisions. A mapping cannot contradict compiler ownership/type invariants even if a human approves it.

Coverage counts use two denominators: observed structural binding groups and observed occurrences. Each has recognized, unresolved, conflicting, unsupported, and discovery completeness. A thousand occurrences of one easy field cannot conceal one unrecognized rare field. Noncoding scalar fields may already be usable without any pairing.

## 6. Resolve raw data before a column exists

C06 starts from structural inventory, not from `conceptCandidates`. Page groups with resource/profile, repeated owner, full extension ancestry, observed value arms, and unresolved reason. Provide bounded authorized examples and exact source paths. A source lacking a recognizable key/value pair must still be reachable.

The first action depends on the reason: missing definition → retry/import request; missing system → explicitly match absent system within a source scope; multiple possible owners → choose/validate ownership; unsupported extractor → explain unsupported capability, not demand a human guess. A mapping does not make unsupported code executable.

The editor chooses observed key/value nodes, repeated owner and applicability. It cannot submit arbitrary query text. Source selection becomes a closed correlated/extension binding validated by `fhir/schema`. An absent system remains explicitly absent; recording a human label is not falsifying the source coding system.

Proposed no-column preview request contains source context token, unresolved-group identity, candidate binding, applicability and bounded example selection. Lifecycle resolves authorized resources and compiles an ephemeral internal one-column workspace using the same compiler and trace semantics. It persists neither an Explorer nor a user column. Preview includes positive and contrasting owner/type/system examples, excluded reasons, binding digest, source context and dependency revisions.

Save revalidates the preview identity and applicability, stores an immutable interpretation revision using the existing interpretation owner, and makes the revision eligible for catalog projection. Do not combine “save interpretation” with “add feature.” The UI offers a separate Find in catalog action. Concurrent contradictory mappings produce a visible conflict, not last-writer-wins. Cancel/stale preview saves nothing. Deactivating a mapping affects future discovery, not retained pinned publications.

## 7. Data preparation order is explicit

Population membership → route/contributor restriction → same-owner extraction → declared time/unit policy in the compiler's supported order → reduction → exact typed category recoding → explicit representation/missingness output.

Finalize and fixture-test unit normalization versus reduction ordering in C05; do not average incomparable raw units then convert. Keep selection of records separate from selection of values. Null, absent, invalid, ambiguous and incompatible unit remain distinct diagnostic evidence even if a chosen output policy emits a missing cell for several of them.

C07 exact recoding is not a regular-expression engine. C08 encoding is an explicit approved vocabulary, not fit-on-all-data learned preprocessing. Roles distinguish ID, predictor, outcome, time anchor, excluded and undeclared. Default migration is undeclared. No predictor matrix silently includes identifiers or outcomes. Research export and matrix export have different eligibility rules but share saved feature intent and execution.

## 8. Check, activation and export

C09 operation identity binds receipt/draft digest, source generation, population membership revision or explicit all-authorized mode, authorization scope, definition/mapping revisions and execution policies. The wire/domain state union is exactly queued, running, complete-pass, complete-fail, incomplete-failure, canceled. Queued can become running or canceled; running can reach any terminal state. A failure before scanning is incomplete-failure with zero scanned rows, not complete-fail. Local idle means no operation exists; stale is a derived mismatch between operation identity and current draft, not another stored operation state. An interrupted worker retries from a supported materialization boundary or restarts its candidate; do not promise row-level resumability if the executor cannot supply it. Browser reload resumes observation of the operation, not a second submission.

Data diagnostics can accumulate across recoverable invalid cells in the same execution engine. Infrastructure/compiler/fatal source errors leave incomplete evidence. Complete-fail means the full authorized population was examined and failed declared policy. Early definite failure must never claim a complete scan. Store bounded examples plus total counts, not every source record in the job object.

Activation is a separate authorized CAS transition on a complete acceptable candidate. Cancel, stale draft, scope change and failed policy activate nothing. Successful publication may reuse a checked candidate only when all bound identities still match. Keep the prior active publication until new activation succeeds. Define candidate lease, retention, cleanup and concurrent cancellation behavior before implementation; exact durations are configuration, not a new scheduler framework.

C10 refresh is propose → compare → explicit apply → recheck. Frozen source membership is not silently expanded on data refresh. C11 exports a pinned publication and explicit scope, never the current mutable draft under an old filename. If source inputs expired but retained artifact bytes exist, serve the authorized artifact; if required retained material is gone, return an explicit unavailable result rather than rebuilding from current data.

## 9. Migration and rollback rules

- Catalog inventory and enrichment are derived, versioned side data. Rebuild/activate without rewriting source or existing publications. Roll back a bad inventory pointer, not raw data.
- Mutable draft changes use the existing versioned migration path. Preserve old column IDs and map old fields to explicit undeclared intent. Test migration twice. Do not reinterpret saved FIRST as a scientifically different reduction.
- Existing configured tables remain editable. Only newly created tables start with zero user-selected columns; do not clear the user's existing columns to demonstrate the new UX.
- Schema/API changes update Go domain, OpenAPI, generated clients and all active writers together. Parse external input once; use discriminated variants internally. Reject impossible combinations instead of nullable bags and silent fallbacks.
- Deploy rollback is allowed only to a binary that can read the written draft/receipt versions. If not, retain the old active publication and stop new writes; document a forward-fix path. Do not promise that reverting a commit reverses data migrations.

## 10. Readiness gates, not hidden assumptions

| Gate | Must establish before dependent code | Owner / result now |
|---|---|---|
| R1 inventory | Existing generation and ACL semantics; resumable grouping/count strategy; query plan for paging/search; compiler hydration without giant snapshot | C01-02, open |
| R2 authoring | Server-local route allocation plus feature addition in one CAS; semantic duplicate rules; recovery after uncertain commit | C01-03, design specified, new behavior unproven |
| R3 definitions | Enumerated supported definition subset; actual CDA metadata availability; import permissions and redistribution constraints | C03-01, open |
| R4 raw mapping | Inventory includes no-candidate structures; binding validator can represent chosen fixture; ephemeral preview cannot persist draft state | C06-01/02, open |
| R5 Check | Candidate retention and transition race contract; recoverable versus fatal diagnostics; executor restart behavior | C09-01, open |
| R6 artifact | Versioned lossless missingness/list encoding and actual Python round trip, including literal marker collisions | C11-02/03, open |

These gates authorize a small experiment inside their WP, not another general architecture audit. Record the result and adjust that WP before writing dependent UI/backend code. No unresolved gate may be relabeled implemented because a mockup or contract validator passes.
