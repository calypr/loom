> Current architecture correction, October 16: the mandatory per-source coverage index and its full-dataset deployment gate are superseded. Existing semantic inventory availability and identity must be independent of optional coverage proofs. Keep the independent census/conformance checks. Exact optional resource/path/auth certificates may suppress raw counterparts; absent/stale/incomplete proof keeps raw fields available. Explicit resource-scoped audits must not rewrite semantic contributions. The stopped CDA rebuild is preserved, not resumed. Read/load speed and write throughput take priority over index size. Earlier source-envelope rollout sections below are historical design evidence, not current implementation instructions.

# Semantic fields from populated data

Implementation plan for Sol low. Written from source at `7dbd59b7de02ad5101bbd5bb28e0626a39ef6400` on `feature/construction-workspace`, 2026-10-16. The matching execution queue is [SEMANTIC_FIELD_COVERAGE_WORKLIST.tsv](SEMANTIC_FIELD_COVERAGE_WORKLIST.tsv).

The initial planning checkpoint changed only these planning artifacts. The user approved implementation on 2026-10-16 and clarified that schema-wide CodeableConcept support is the target; the examples below are illustrative. Execution status is tracked in the matching worklist. No product behavior or runtime result was established by the planning checkpoint. Source inspection is evidence of implementation shape, not a passing runtime test. Existing tests named below were inspected or located, not run. Dataset counts and current performance are unmeasured.

## 1. Decision and scope

Keep the existing profiler, semantic inventory, compiler bindings, construction choices, and Builder picker. Add occurrence coverage evidence to the inventory lifecycle and a small, contextual presentation result to the existing semantic browse API. Use that result to group proven raw counterparts under semantic fields in the normal picker. Preserve every physical selector and every saved raw-column definition.

Ship the first complete increment for a root Specimen table, then prove diverse families and related-source contexts. Do not wait for universal FHIR support to improve the picker. Do require a census of the selected generation that classifies every populated coded structure as semantic, retained raw, or unsupported with a reason. “Every structure is classified” and “every structure is semantically supported” are separate claims.

The following field meanings are examples, not a resource or path allowlist.

The implementation target is every CodeableConcept-bearing case in the supported FHIR schema. Discover members from schema datatype relationships across all resource types, nested datatypes, repeated parents, extensions and choice arms. Use generic structural rules rather than resource-specific branches. Include direct Coding and primitive codes where their schema metadata establishes those types. A schema conformance inventory must enumerate every reachable CodeableConcept member template, handling recursive type relationships without infinite expansion, and exercise its discovery with generated witnesses. Dataset coverage then proves which of those structures are populated and whether every populated occurrence is represented correctly. Schema conformance alone does not prove dataset completeness or current-table applicability.

Specimen is the first integration witness, not the end of implementation. Completion requires the full schema inventory and every observed coded occurrence to be accounted for. Retained or unsupported cases must be individually justified with a concrete compiler limitation and a regression fixture; they cannot be a blanket escape from implementing generic CodeableConcept handling. Unpopulated schema members remain absent from the picker.

Illustrative field meanings:

| Data | Field | Value and identity |
| --- | --- | --- |
| Observation code is Height, valueQuantity is 170 | Height | 170 with its unit, source occurrence, and code identity |
| Specimen type is Tumor | Specimen type | Tumor, with its original coding metadata |
| Condition clinicalStatus is Active | Clinical status | Active; it is not a column named Active |
| Medication code uses a `compound_name` system | Compound name | CARBOXYAMIDOTRIAZOLE or its recorded display; the system and exact version select the family |
| Identifier has a namespace and value | Identifier named by its namespace | The identifier value; Identifier stays a distinct datatype |
| Populated noncoded scalar or supported repeated field | Its readable field name | Its existing raw value and existing column form |

Do not turn coding aliases into unrelated measurement columns or extra rows. Do not infer terminology equivalence from matching display text, co-occurrence, or a common system URI. A semantic value can have several recorded representations. Until a representation policy is proved, keep exact system/version selections visibly distinct and retain raw access.

Non-goals for this milestone: unpivot or list-reshape support, changing ALL to scalar, automatic unit conversion, terminology mapping, external terminology services or LLM label inference, a universal field framework, a new API version, a second compiler, general route-search redesign, package moves, schema cleanup, automatic rewriting of saved queries, and broad verifier repair. No per-dataset URI hardcodes. Unpivot remains a secondary analysis/export tool. Its duplicate selector and single-valued-input limitations need a separate bounded workflow.

## 2. Source facts and gaps

The active source is `/private/tmp/loom-construction-implementation`. `/Users/peterkor/Desktop/BMEG/loom` is a different dirty checkout and is excluded from implementation and commits. GitNexus was attempted by the parent and reported that the repository was unavailable through this MCP server. Repeated index refresh attempts have hit a registry-lock sandbox failure. This plan uses source evidence, with no Graphify fallback. Retry GitNexus only when its availability changes; do not turn this milestone into graph setup work.

`docs/PACKAGE_AUDIT.csv` assigns observed facts to `internal/catalog`, compiler semantics to `internal/fhir/schema` and `internal/dataframe/compiler`, and application orchestration to `internal/explorer/lifecycle`. `docs/PACKAGE_AUDIT_DECISIONS.csv` and `docs/EXPLORER_BACKLOG.md` were consulted. Their broad historical audit and lifecycle entries are not proof for this change. No package boundary change is proposed.

| Stage and existing owner | Source fact | Consequence for this change |
| --- | --- | --- |
| `internal/catalog/write_profiler.go`, `Profiler.observePayload`, `Documents` | Extracted field values increment `doc_count`; field and enrichment retention have limits. | Reuse populated field evidence. `doc_count` is neither a Coding count nor a value count. Do not substitute schema discovery for populatedness. |
| `internal/fhir/semantic/member_walk.go`, `WalkMembers`, `MemberFact`; `walk.go`, `Occurrence` | Walkers retain concrete owner paths, repeated boundaries, schema datatypes, choice metadata, and raw JSON. `WalkMembers` returns structural diagnostics. Unknown members receive diagnostics rather than recursive schema resolution. | Build the denominator before semantic-rule matching. Account for unknown subtrees separately. Preserve repeated-parent identity. |
| `internal/catalog/semantic_schema.go`, `observeSchemaSemantics` | Identifier and Extension have datatype associations. Coded-value inference currently requires exactly one nonrepeated CodeableConcept anchor in the owner and at least one populated choice value. Standalone Coding requires nonblank system and code; Coding descendants of Identifier/Extension and paired concepts are skipped. | These rules cannot define their own completeness denominator. Missing systems, text-only concepts, ambiguous anchors, nested coded values, and skipped descendants need explicit dispositions. |
| `internal/catalog/semantic.go`, `observeSemanticObservations` | Generated-index failure returns silently; the semantic-walk error is discarded. `observeSchemaSemantics` also discards walker diagnostics. | A new coverage-complete state must never survive an unreported walk failure. Propagate or record errors through the existing emission/backfill lifecycle before enabling grouping. |
| `internal/catalog/semantic_inventory.go` | Existing contribution rows include source locator, auth path, generation, build, concept and binding IDs. Emitter calls the same semantic rules without bounded field-summary retention. Builds distinguish inventory schema, observation schema/rules, entry index, emitter, source kind and source availability. | Reuse these identities and lifecycle. Keep binding identity stable when only coverage evidence changes. |
| `internal/catalog/arango/semantic_inventory.go` | Indexed pages and bounded selection resolution are authorization-scoped. `SYSTEM_VERSION_CATEGORY` groups standalone `CODED_CONCEPT_V1` by binding, source path, system and exact version. Family population sums emitted events. | Family population does not prove how many source concepts, resources, or rows exist. Use bounded resolver reads for presentation, not a second semantic inventory. |
| `internal/ingest/semantic_inventory_backfill.go`, `BackfillSemanticInventory`; `generation_workers.go`; `generation_orchestration.go` | Existing ingestion and retained-source keyset backfill have deterministic contribution writes, leases, checkpoints and completion. | Add coverage evidence to the same pass and completion bracket. Never scan source resources on a picker request. |
| `internal/explorer/authoringv2/semantic_command.go`, `ResolveSemanticSelectionPlan` | Compiler-ready plans exist for Identifier, Extension, CODED_VALUE, CODED_CONCEPT and CODED_CATEGORY. Coded-value selections reject nonempty coding versions; coded categories support exact version via Coding-scoped binding. | Do not advertise all discovered rules as executable. Versioned measurement values remain retained raw/unsupported until an explicit compiler change is justified. |
| `internal/fhir/schema/correlated.go`, `CorrelatedBinding`, `CorrelatedKey`, `ExtensionBinding`; `identifier.go` | Typed bindings preserve owner, key, value scope, choice arm, fallback, version, units and extension ancestry. | Extend these existing bindings only for a demonstrated gap. No independent extraction language in UI or coverage code. |
| `internal/explorer/lifecycle/semantic_catalog.go`, `BrowseSemanticInventory`, `semanticInventoryChoicesForOutput` | Browse separates inventory readiness from route proof and can filter entries for the current output. The output helper reuses proofs within a request but loops through route pages until candidates resolve or routes exhaust. Its semantic context token does not include output/draft identity. | Coverage cannot reuse that token as proof for the current table. Do not call full route paging over the census. Bind new presentation proof to output and document state, and use a direct root or exact selected occurrence. |
| `internal/explorer/lifecycle/construction_routes.go`, `SearchConstructionChoices` | Signed source identities and exact `occurrenceId` resolution already exist. Related FIELD search permits scalar required/optional-one fields without repeated boundaries. | Reuse route authorization and compiler proof. Keep the related-field support guard. Coverage does not grant application eligibility. |
| `internal/server/explorer_capability.go`, `capabilityEvidenceFromCatalog` | Capability field observations derive `Populated` from positive document counts and retain compiler facts. | Keep this source for raw eligibility. Coverage supplements it; it does not replace or erase candidates. |
| `catalogItems.ts`, `fieldCatalogItems`, `catalogChoiceIntent` | Picker requires `populated === true`, validates choice identity, and excludes unsupported repeated related FIELD choices. Root repeated fields remain. | Keep those guards. Hide/group only after both coverage and current-context eligibility checks. |
| `BuilderWorkspace.tsx`, `ConceptCatalog.tsx`, `CatalogSelectionOptions.tsx` | Coded values and Other fields already exist; Builder uses `SYSTEM_VERSION_CATEGORY`. Choices, pagination and table-context cancellation already have owners. Exact related sources also use `RelatedFieldEditor`. | Evolve these flows. Preserve one source chooser and one Add/Save lifecycle. Cover both picker entry points; do not wire only the root tab. |

Further source facts matter for presentation. `semanticCategoryLabel` currently prefers a readable system-tail label before falling back to resource/path. That works for `compound_name`, but does not prove that every readable system tail is the best field name. Use schema title/path for the structural field, use a system label as a qualifier unless its field interpretation is evidenced, and retain identity when labels collide. A category-conditioned title requires a proved binding and context, not an arbitrary sibling category. `semanticValueProjectionAssociations` projects only selected parts of structured values, such as Period.start or Ratio.numerator.value. Such a scalar does not cover every populated part of the source object.

Assumptions to check at SC-00/SC-01: retained CDA source matches generation `cda-fhir-v1`; source availability can be independently verified; current field enrichment is complete for the selected scope; Specimen type has at least one supported exact family; the source contains the examples reported by the user. Do not manufacture counts or universal support if any assumption fails.

## 3. Chosen augmentation and alternatives

Use one coverage adjunct owned by `internal/catalog`, persisted by `internal/catalog/arango`, and built by the existing ingestion/backfill pass. It stores proof and reasons, not selectable fields. Reuse the existing inventory family/concept references as replacements. Add a contextual `fieldPresentation` result to `SemanticInventoryBrowseResponse`; its new request input is explicit and optional for older clients. Keep current entries and construction-choice APIs intact.

Existing capability annotations are the right home for dataset-wide facts, but not the final grouping decision. They lack the current output, exact source occurrence and row-operation context. Store coverage with the inventory and read it into lifecycle presentation. Do not add a contextual boolean such as `hideRaw` to persisted field facts.

Create one dedicated derived collection, `fhir_semantic_coverage`, because bounded field summaries and emitted semantic contributions do not contain the independent denominator. Use explicit source-contribution and path-summary record variants, indexed by project, generation, build, authorization path, resource, record kind and path. Materialize summaries only after source contributions persist. Reuse existing storage batching, claims and cleanup conventions. This collection contains coverage evidence, not copied patient payloads or a new raw-value catalog. Any evidence requiring a different storage design returns to Astra; Sol does not colocate these records in another collection.

Persist one aggregate per source locator and canonical path, containing exhaustive counts/dispositions, reason counts, bounded replacement references and bounded diagnostic occurrence locators. Reconcile individual occurrences and facets while processing that source, then discard that temporary occurrence state. Do not retain an entire page's occurrence graph or write a durable row per Coding. Use explicit limits for per-source working memory, aggregate size, replacement references, diagnostic locators and write batches. If an oversized source prevents exhaustive reconciliation, mark its proof incomplete and retain raw choices. Truncating replacement references or covered facets invalidates grouping for the affected path. Truncating diagnostic examples alone does not invalidate exhaustive counters or reconciliation. Record these as different conditions. SC-02 must measure source reads, peak working memory, contribution/summary row counts and stored bytes against the existing inventory baseline on the same fixed source set; confirm that repeated Codings grow occurrence counters without creating one durable coverage row per Coding.

Rejected alternatives:

| Alternative | Why reject it |
| --- | --- |
| Hide `.coding` or `.code` paths by string prefix | Erases text-only concepts, metadata, unknown arms and unsupported table routes. It has no occurrence proof. |
| Call inventory “complete” when all emitted entries have plans | Counts only what current rules already discovered; omissions are invisible. |
| Preview/propose every field, or page every route on each click | Converts browsing into data execution, repeats expensive work and recreates request loops. Population is an index fact. |
| Rebuild all indices on every draft/filter change | Data evidence is generation-scoped; authoring context should only invalidate presentation applicability. |
| Create a universal semantic field engine/API v3 | Duplicates typed bindings, saved identities, auth rules and UI selection lifecycle. Existing categories already cover the first improvement. |
| Remove physical paths or migrate saved raw columns automatically | Changes saved query meaning, provenance and export contracts. Grouping is a picker presentation change. |

## 4. Coverage denominator and typed contracts

The sketches in this section are proposed types, not claims that these symbols exist. Existing symbols retain their names and wire meaning. Use Go typed enums plus validated constructors/variant payloads; use OpenAPI `oneOf` with discriminators and the existing Zod boundary in `ui/packages/loom-ui/src/types.ts`. The repository currently generates Go from OpenAPI and maintains UI Zod schemas by hand. Keep that existing boundary, infer TypeScript types from Zod, and verify matching wire examples against both contracts. Do not introduce a TypeScript generator for this work. Any generated contract artifacts must derive from their authoritative schema. Do not use casts or optional-field bags to turn partial evidence into proof.

### Independent census

For each retained resource, census present structures before applying emission rules. Reuse `WalkMembers` datatype and owner facts, with a narrow raw structural traversal of unknown subtrees and a reconciliation against observed populated paths. This second traversal inventories source shape only; it must not invent semantic bindings. Detect unknown coding-shaped objects as unclassified structures with reasons, not as confirmed FHIR Coding. Preserve walker diagnostics.

The denominator includes CodeableConcept occurrences, Coding occurrences, known primitive coded members, Identifier occurrences, Extension value occurrences and unclassified populated structures. Keep counts by kind. A CodeableConcept with three Codings is one concept group and three coding occurrences, not four interchangeable values. Identifier is reported separately. Detect primitive codes from authoritative schema binding/type metadata. `schema.Element` currently exposes binding metadata and JSON types but no direct universal primitive-code enum flag. Prove metadata coverage; when it cannot distinguish a code from ordinary string, retain the field and report that classification limitation. Do not guess from names.

Define a stable audit occurrence locator as source kind + source ID + concrete owner path + concrete member/array index + kind, scoped by project, generation, auth path and schema identity. The index is meaningful only inside an immutable source generation. Canonical `component[]` paths are aggregation keys, not occurrence identity. Two parents with the same code and value remain two owners. Represent missing, explicit null, blank string, false, zero and empty collection separately. False and zero are populated. Blank values and metadata-only concepts remain accounted for even if not meaningful selectable values. Missing/null/empty evidence is recorded separately from the nonempty value denominator. Existing field `populated` semantics must not be silently changed in this work.

Each occurrence receives exactly one primary disposition:

```ts
type Disposition =
  | { kind: 'SEMANTIC'; references: NonEmpty<SemanticReference>; roles: NonEmpty<CoveredRole> }
  | { kind: 'RAW_RETAINED'; reason: CoverageReason; rawPaths: readonly string[] }
  | { kind: 'UNSUPPORTED'; reason: CoverageReason; sourcePaths: readonly string[] };

type SemanticReference =
  | { kind: 'EXACT'; conceptId: string; bindingId: string }
  | { kind: 'FAMILY'; reference: ExistingSemanticInventoryFamilyReference }
  | { kind: 'NATIVE_FIELD'; candidatePath: string; resourceType: string };

type CoveredRole = 'VALUE' | 'CONCEPT_KEY' | 'CODING_METADATA' | 'UNIT' | 'REFERENCE';
type NonEmpty<T> = readonly [T, ...T[]];
```

`SEMANTIC` means the structure has a proved interpretation through existing bindings, not that it can be added in every table. `RAW_RETAINED` means an executable populated raw representation remains. `UNSUPPORTED` means the compiler cannot expose the structure faithfully yet. Keep its source path and diagnostic in the coverage report. A structure may have subsidiary uncovered facets, so full path grouping requires a separate facet check. Do not let one supported Coding mark its containing CodeableConcept or all sibling Codings covered.

Use a closed reason vocabulary, initially `MISSING_SYSTEM`, `MISSING_CODE`, `TEXT_ONLY`, `AMBIGUOUS_ANCHOR`, `MIXED_CHOICE`, `UNSUPPORTED_VALUE_PROJECTION`, `UNSUPPORTED_VERSION`, `UNKNOWN_SCHEMA_MEMBER`, `UNKNOWN_CODE_CLASSIFICATION`, `UNSUPPORTED_SELECTOR`, `INCOMPLETE_SOURCE`, `WALK_FAILED`, `TRUNCATED_EVIDENCE`, and `UNREPRESENTED_METADATA`. Keep explanatory text separate. Unknown enum values fail validation at the boundary and fall back to retaining raw fields; they never default to complete.

Per-source matching compares the census to emitted bindings using owner identity, key identity, value/choice selector and fallback policy. Do not match on labels or values alone. A many-to-many relation is expected: equivalent representations can select one owner value, and one semantic binding can cover many occurrences. The check is set membership of source occurrences/facets, not `sum(family.population) == doc_count`.

The deterministic report must give sorted per-resource/path/family dispositions, owner and Coding counts separately, source-record counts, reason counts, bounded source locators for failures, missing-source diagnostics, schema/rule/build identities and a content digest. Do not store raw identifier or clinical values in the default report. Full claim: every enumerated source occurrence is classified, zero unclassified occurrences, and independent source-count reconciliation is complete. This does not imply zero retained/unsupported structures.

### Persisted proof and replay

```ts
type CoverageBuild =
  | { kind: 'UNAVAILABLE'; reason: CoverageReason }
  | { kind: 'BUILDING'; identity: CoverageIdentity; checkpoint: ExistingCheckpoint }
  | { kind: 'FAILED'; identity: CoverageIdentity; reasons: readonly CoverageReason[] }
  | { kind: 'COMPLETE'; identity: CoverageIdentity; sourceReconciliation: VerifiedCounts; reportDigest: string };

type CoverageIdentity = {
  project: string; datasetGeneration: string; inventoryBuildId: string;
  sourceKind: string; schemaIdentity: string; coverageRuleVersion: number;
};
```

Authorization-path partitioning remains on every stored contribution and summary. Scope-specific reports and API counts aggregate only authorized partitions. A global complete build can certify traversal completion without exposing global counts, locators or examples to restricted users. A restricted-path scan must not masquerade as a global census.

Persist idempotent source/path aggregates before advancing the existing keyset checkpoint. Source rows with no semantic emissions still require a census contribution. Replayed pages replace deterministic keys, rather than incrementing counters again. Materialization must use only the completed source set for the exact build. Incomplete counters, reconciliation, replacement references or facets cannot authorize grouping. Bounded diagnostic-example truncation remains compatible with exhaustive proof and must not force unnecessary raw fallback. An interrupted or failed build leaves raw fields visible. Test both truncation classes and failures between semantic write, coverage write, checkpoint and summary materialization.

Coverage-rule revision invalidates coverage summaries. Add its version to `SemanticInventoryBuildID` when the coverage adjunct first ships, so the current completed-build fast path cannot skip the new census. This deliberately creates one new derived build and one bounded backfill for an unchanged generation, while preserving concept/binding identity. Future coverage-only revisions use that same explicit version, not an observation-rule bump. Emission-rule or grammar/schema changes may also require a new inventory build through existing version conventions. A presentation-only change does not change build or semantic binding IDs. Data ingestion into a new generation requires new evidence. A draft edit, filter, group or tab switch requires no data reindex. Retained old generations/saved bindings remain resolvable under existing compatibility policy. Do not casually increment `SemanticObservationRuleVersion`: `ResolveSemanticSelectionPlan` rejects unsupported versions and saved-frame compatibility is explicit.

### Current-table presentation proof

Add a request variant to the existing semantic browse operation for a bounded batch of current raw candidate IDs, the exact output/document identity, and source context. Reuse the existing occurrence route identity. Require output and draft version/digest when this variant is present. Use current server workspace state to verify them. Limit the candidate batch and replacement set, starting from the existing 50-item browse and 100-selection limits where appropriate. Final numeric batch caps are explicit contract limits, not claims about measured performance.

The corresponding additive response envelope is conceptually:

```ts
type FieldPresentation =
  | { kind: 'UNAVAILABLE'; reason: CoverageReason | ContextReason }
  | {
      kind: 'READY'; context: PresentationContext;
      decisions: readonly RawPresentationDecision[];
      replacements: readonly ExistingSemanticInventoryItem[];
    };

type RawPresentationDecision =
  | { kind: 'KEEP_RAW'; candidateId: string; reason: CoverageReason | ContextReason }
  | {
      kind: 'GROUP_UNDER_SEMANTIC'; candidateId: string; coverageId: string;
      replacementIds: NonEmpty<SemanticItemIdentity>;
      access: 'SOURCE_FIELDS';
    };
```

`PresentationContext` includes project, authorization scope digest, snapshot token, generation, inventory/coverage build IDs, explorer/output, draft version/digest, selected source node and exact occurrence/route identity, and selected construction stage where relevant. It is server-validated. Existing `semanticInventoryContextToken` alone is insufficient. Result and cursor/cache identity must also bind the requested candidate batch and presentation revision. The client cannot claim proof by echoing an arbitrary digest.

All required replacement IDs must resolve to authorized existing inventory items with current compiler-supported choices and accessible source fields. Enforce this in construction/validation, not by non-null assertions. Context reasons are a closed set including no exact route, unsupported current stage, stale context and unavailable replacement. Missing envelope from an older server is decoded as unavailable and retains raw. Every candidate omitted because of a bound or unknown status also retains raw. No negative decision is inferred from an empty or partial response. Retention never overrides eligibility: normal raw visibility is positive populated evidence AND addable in the current table AND not safely replaced. Unsupported repeated related raw fields stay excluded, with their evidence/reason retained in the report.

Population evidence and semantic replacement links come from indexed coverage reads. Current applicability comes from existing root choice proof or reauthorization of the exact selected occurrence, with shared proof for equal candidate/route inputs. Do not invoke a preview, proposal, data scan or exhaustive route search to construct these decisions. For a related source without one selected exact route, keep raw and let the existing explicit route-selection flow settle the route. For grouped/pivoted/source-expanded outputs, use existing construction capability rules; where the proof does not establish application at that stage, keep raw. Never relax the scalar related FIELD guard as part of this change.

The presentation request is independent of semantic search text and page cursor. The response carries the existing semantic item records needed to make replacements reachable without paging through the inventory. This is a bounded supplemental view of the same inventory references, not a second inventory or routing service. Cache it once per source/table context. The ordinary semantic page remains paginated. Do not fetch all pages merely to construct coverage. Requests may be partitioned by the visible raw-field batch; the UI must not eagerly walk all batches.

## 5. What grouping means

Grouping removes duplicate choices from the default list, not information from the model. Exact raw fields remain available under each semantic field's “Source fields” disclosure, using the existing FIELD candidate and choice. Existing saved raw columns retain their original IDs, selectors, reduction, null handling, filters and labels.

The semantic display string is not byte-identical to every raw field. `display` with a `code` fallback changes behavior for missing or blank display. A family label does not expose every code, version or `userSelected` value. Therefore each grouped path must have an explicit access role:

| Path/facet | Minimum condition before grouping |
| --- | --- |
| Display/value leaf | Complete occurrence mapping to the semantic value, including fallback and list/reduction behavior; source field remains explicitly selectable. |
| Coding code/system/version | Recorded identity remains inspectable and the exact raw field remains selectable from Source fields. Do not claim the displayed label is the code. |
| `userSelected`, CodeableConcept text, extensions or other metadata | Retain in Other fields until exact source-field access is attached to the correct semantic family/group and all occurrences are accounted for. No subtree prefix suppression. |
| Quantity unit/system/code/comparator | Preserve quantity/unit binding and exact source fields; numeric value alone covers none of these metadata leaves. Mixed units do not become comparable automatically. |
| Identifier namespace/type/assigner/period | Namespace/value pairing covers the value family only. Retain independently populated metadata unless the correct family has complete access. |
| Reference and structured-value siblings | Keep separately unless the existing semantic binding actually represents them and their source access is proved. |

Multi-system families are distinct selectors over one concept group, not evidence of unrelated clinical concepts. Present them within one structural field group with system/version qualifiers where the occurrence evidence proves that grouping. Preserve simultaneous recorded representations and list semantics. No automatic “preferred terminology,” no display-label merge, and no row explosion. Exact duplicate Coding representations may be deduplicated only according to existing tested compiler semantics; do not deduplicate distinct owners.

Search and pagination rules are mandatory. Searching a raw path must find its semantic field or a direct “Included in …” link. The link switches to Coded values and focuses the exact replacement card from the current presentation response; it does not start route paging. Other fields need not render duplicate semantic cards to hide their raw counterparts. Replacement cards on a different semantic page remain reachable by identity. If this reachability cannot be established, retain raw. Back/forward page navigation, tab changes and cached results must not alter proof. Identity includes source scope even when labels match.

Unknown/error states show usable raw fields and a short reason when relevant. Unsupported structures are available in coverage details, not advertised as addable columns. Do not fill the normal picker with disabled schema possibilities. Keep noncoded populated fields such as ID, dates, descriptions and supported references. Known primitive enums can use existing FIELD commands with meaningful labels and categorical presentation; no new coded-value binding is needed merely to display a native enum.

Use the existing query and construction lifecycle owner for loading/cancellation. The touched `ConceptCatalog` flow currently contains effects for context/tab resets and initial selections. Replace synchronization in the touched flow with context-keyed state and explicit owning actions, plus the existing query subscription. Do not move it to `useLayoutEffect` or refactor unrelated effects. Late results must never group a field after source, output or draft context changes.

## 6. First complete increment and subsequent scope

SC-00 through SC-05 form the first end-to-end unit. Run the census first, then implement the proof and picker for the supported root category pattern. The target is a fresh owned QA Explorer in `loom_dev_cda_fhir`, generation `cda-fhir-v1`, with Specimen as the row root. Do not change `loom-dev-bootstrap` or the user's draft. The existing QA clone is not permission to erase or replace it; use the verifier's ownership rules. Explorer has no DELETE API.

Expected before/after, subject to the source census:

| Before | After |
| --- | --- |
| Coded values can offer a Specimen type category; Other fields also lists populated `type.coding[].code`, `.display`, `.system` and any version leaf as independent generic choices. | Coded values offers “Specimen type” with exact system/version qualifier and honest scalar/list label. Its values include Tumor when the independent source has it. |
| A user must infer which generic coding path represents the intended category. | Other fields keeps ID and populated noncoded fields. Proven coding counterparts move under Specimen type → Source fields. Uncovered text/metadata or partial families stay visible. |
| A semantic family label might be mistaken for a value column name. | Tumor appears as a value/example, not as a new column named Tumor. Selecting the field uses the existing Add/Save construction flow. |

If even one authorized populated type occurrence lacks semantic interpretation or source-field access, do not hide the entire raw path. The first increment can still improve labels and ordering while retaining the unresolved path. Report the concrete reason and next bounded fix. Do not hardcode the CDA system or Specimen path into a universal suppression list.

After the first native lifecycle passes, close these distinct patterns in order of observed gaps:

1. Condition clinicalStatus and Medication compound name, including versioned/unversioned and missing-display cases. They exercise categories rather than Observation measurement-only logic.
2. Observation height/other measured values, repeated components and choice arms. Prove key/value/unit correlation. Ambiguous scalar anchors remain explicit until a schema-backed relation settles them. The current “exactly one scalar concept” rule is not proof that the lone concept has the intended clinical role; do not bind an arbitrary sibling concept to a value.
3. Identifier namespaces and nested Extension URL/value ancestry. Preserve nested code representations and reference/structured-value siblings; support a new projection only if existing bindings can express it and a source fixture proves it.
4. Native primitive code fields, unknown populated fields, partial source coverage, and related-source/current-table contexts. Every remaining occurrence gets a disposition even if no new semantic projection is justified.

No universal ALL-FHIR completion claim is expected. Completion names the generation, schema/rule versions, classified denominator, supported patterns, retained patterns and unsupported reasons. New source shapes invalidate the applicable proof and fall back to raw until classified.

## 7. Fixture and executable-check matrix

Build fixtures before suppression. Literal expected outcomes must come from independently constructed JSON and explicit owner locators, not the emitted inventory or a captured preview response. For CDA, use bounded independently read source witnesses and a generation census; inventory examples are not an independent oracle. Keep realistic repeated structures, not only one Coding per resource.

| Case | Required assertion | Closest existing owner / new test location |
| --- | --- | --- |
| Height 170 cm plus another code/value in a different component | Exactly the intended owner value and unit; no cross-parent combination; record IDs preserved | `internal/catalog/semantic_test.go`; compiler `correlated_arango_test.go`; new coverage fixture in `internal/catalog/semantic_coverage_test.go` |
| Specimen Tumor and normal category values | One field family, different values; all covered Coding occurrences map to the exact family | `semantic_inventory_category_test.go`; `internal/explorer/lifecycle/semantic_category_test.go` |
| Condition status plus method/category/value siblings | Structural labels stay distinct; ambiguous anchor does not acquire an arbitrary value | `semantic_inventory_emitter_anchor_test.go`; new ambiguous-anchor coverage cases |
| Medication compound_name with two codes, absent display, blank display | Exact system/version and same-Coding fallback; code remains inspectable | `internal/catalog/arango/semantic_coded_category_integration_test.go`; `internal/dataframe/compiler/correlated_system_version_arango_test.go` |
| Same concept represented by several systems, repeated aliases and conflicting display labels | No inferred equivalence, no duplicate owner rows, no lossy cross-system merge; same-label distinct bindings stay distinct | `correlated_physical_test.go`; new census/grouping fixture |
| Missing/blank system, missing/blank code, text-only concept, metadata-only concept | Census retains them; exact reason; no whole-path grouping when only other siblings emit | `semantic_test.go`; `semantic_coding_display_projection_test.go`; new coverage fixture |
| Missing, null, blank, false, zero, empty list, one value and several values | Distinct population states; honest list cardinality; no truthiness loss or implicit FIRST | `write_profiler_test.go`; new coverage tests; existing `ConceptCatalog.unit.test.tsx` or new `catalogItems.unit.test.ts` |
| Version missing/null/empty/nonempty | Preserve actual existing matching semantics, separate exact version from unversioned, retain unsupported versioned CODED_VALUE | `correlated_physical_test.go`; `correlated_selection_test.go`; lifecycle version tests |
| Direct Coding such as Task input plus repeated owner | Coding-scoped selection and owner identity; no requirement for `.coding[]` spelling | catalog standalone Coding tests; `TestDirectCodingObservationIssuesAndConsumesExactCompilerChoice` |
| Identifier with two systems and values, type/assigner/period | Named value families; no mixing namespaces or converting identifiers into concept keys | Identifier schema/authoring tests; new coverage tests |
| Nested Extension, same leaf URL under different parents, valueCoding/valueCodeableConcept/valueReference | URL ancestry and choice arm preserved; unresolved nested code metadata retained | `semantic_schema_test.go`; compiler extension tests; new coverage tests |
| Quantity, Period, Range, Ratio, mixed units, comparator, valueReference | Selected scalar does not certify unprojected siblings; unit/ref metadata remains accessible | existing correlated/quantity schema tests; new structured-value coverage fixture |
| Native enum and unknown string/object/array member | Generated binding evidence used; unknown subtree is reported even if no semantic rule emits | `internal/fhir/semantic/member_walk_test.go`; new raw-census reconciliation tests |
| Old generation, wrong auth path, restricted empty scope, source absence, interrupted/replayed scan | No count/example leakage; no false complete state; deterministic replay; raw fallback | catalog Arango inventory tests; `internal/ingest/semantic_inventory_backfill_test.go` and integration test |
| Partial enrichment or coverage limits, mixed good/bad family, new unclassified field | Partial proof cannot suppress whole raw path; known supported fields remain usable | new coverage aggregation and lifecycle presentation tests |
| Current root vs related source, two routes to same resource, selected source expansion, Group/Pivot/Filter/Join/Append | Coverage remains data evidence; exact stage/route/application check controls grouping; no guard removal | `construction_routes.go` tests; `outputEligibility.unit.test.ts` and `ConceptCatalog.unit.test.tsx`; new source-projection regression if needed; selected native cases |
| Semantic item on another page; search for raw path; source/tab change; late response | Replacement remains reachable; stale/missing envelope retains raw; no extra route or preview loops | `ConceptCatalog.unit.test.tsx`; new presentation tests; `types.unit.test.ts` |
| Existing raw draft column and old semantic frame | Reload/edit/filter/publish produce identical values and source bindings; no migration on browse | `semantic_frame_compatibility_test.go`; `correlated_legacy_compat_test.go`; native saved-column lifecycle |
| Viewer and CSV/ZIP export after new semantic selection | Values, multiplicity, names, units and source explanations match the selected publication; old publication unchanged | existing Viewer/exports registered cases selected by actual changed boundary |

Existing focused commands, all from repository root unless stated:

```bash
go test ./internal/fhir/semantic ./internal/catalog -count=1
go test ./internal/explorer/authoringv2 ./internal/explorer/lifecycle -run 'Semantic|CodedCategory|CodedConcept|DirectCoding' -count=1
go test ./internal/catalog/arango -run 'SemanticInventory|Semantic.*Category' -count=1
go test ./internal/ingest -run SemanticInventory -count=1
go test ./internal/dataframe/compiler -run 'Correlated|Identifier|Extension' -count=1
make generate-openapi
make openapi-check
npm --prefix ui/packages/loom-ui run build
```

Choose the smallest relevant subset for the unit, not every command after every edit. Arango-backed tests can skip when their environment is absent; record skip as a gap, then run the required test in the assigned owned runtime. Add new `TestSemanticCoverage...` and `TestFieldPresentation...` names under the owners above and use exact `-run` expressions after they exist. Their names are proposed, not existing checks. Avoid blanket `npm test`, which includes the entire Vitest suite; extend the existing registered focused-check group for the affected UI tests.

Actual existing native registration:

```bash
node scripts/run-native-verification-bracket.mjs \
  --scenario basic-standalone-coded-fields --case coded-fields --checks-only
```

For implementation workers, invoke the registered command through the assigned-check guard from a physically distinct worktree:

```bash
node scripts/verify-ui/helpers/run-assigned-check.mjs \
  --expected-root /absolute/owned-worktree \
  --forbidden-root /private/tmp/loom-construction-implementation \
  -- node scripts/run-native-verification-bracket.mjs \
  --scenario basic-standalone-coded-fields --case coded-fields --checks-only
```

The registry maps this case to `scripts/verify-ui/specs/standalone-coded-fields.spec.mjs`, title `Basic fixture adds standalone coded fields with exact Coding version and display correlation`. Its literal fixture spans Patient, Condition, Observation, Specimen, MedicationAdministration and Medication, including versioned/unversioned Coding and a text-only Condition control. Its registered status is currently untested, not evidence of a current pass. Focused prerequisites include `standalone-coded-field-fixture.test.mjs`, `ConceptCatalog.unit.test.tsx` and `types.unit.test.ts`.

The assigned-check guard also accepts a preflight without a trailing command. Use that identity check before direct Go/build commands in the isolated worktree; its wrapped-command allowlist does not include arbitrary Go or TypeScript commands. Do not change the guard to bypass the restriction. The registered checks-only route owns Vitest argument policy.

Fix a known stale driver before its first full run. `scripts/verify-ui/workflows/standalone-coded-fields.mjs` still looks for `construction-choice-proposal-panel` Cancel/Apply around lines 452–471, and the registry still requires the obsolete second-confirmation `previewCancel`. Current product behavior uses one Add/Save action. Update only that driver and its registry/lifecycle expectation to the current action. Preserve the independent expected values, exact Coding version/correlation, persistence/reload, and cancellation before submission with no draft write. Do not restore a duplicate confirmation panel to satisfy the test. Treat this as a scoped harness correction, with its own focused check, before product browser verification.

Extend this case for the first generic picker proof, and add a narrowly registered CDA semantic-coverage case only where real-generation coverage cannot be expressed in the Basic case. Do not invent a runnable scenario ID before registration. Use `.codex/skills/verify/SKILL.md` to select the full bracket and validated target. Existing `basic-related-expand-field-lifecycle/observation-root-medication-administration` proves a related-source/active-record Group lifecycle and is appropriate when that entry point changes. Existing `basic-coded-pivot/integer-lifecycle` and `standalone-reshape-coded-pivot/{coded-pivot-integer,coded-pivot-string}` apply only if their binding/availability behavior changes. Do not rerun unrelated reshape cases.

A full UI lifecycle includes the current single Add/Save action, cancellation before submission with no write, edit, removal, reload, source switching, and exact independent values. Keep preview/apply assertions only for operations that actually retain those stages. For the first increment, include selecting a raw source field from the semantic disclosure and renaming/removing/reloading that saved raw column with its physical binding unchanged. The existing root UI has no raw-binding selection editor; do not add one for this refactor. Prove initial raw selection retention in focused UI tests and use the existing related-field edit entry in SC-09 for saved raw binding reopening. A presentation unit test or compiler pass does not establish this lifecycle. Publication/viewer/export checks are required for any changed execution, fallback, column identity or binding meaning. If the first increment leaves those untouched, retain the existing compatibility tests and run one bounded published semantic-field witness at final integration to prove the end-to-end contract.

## 8. Execution units and ownership

The TSV is authoritative for IDs, dependencies and task ownership boundaries. SC-00–05 is the first vertical slice, followed by the datatype and contextual units and final acceptance. SC-11 adds bounded census dispatch independently; SC-12 supplies the required embedded-resource compiler support after the first slice. The first slice has narrowly split tasks because census, persistence, contract, picker and browser ownership are distinct. These are the implementation tasks now authorized by the user; close each required gate before marking it complete.

| Unit | Outcome and boundary | Depends on |
| --- | --- | --- |
| SC-00 | Recheck source/runtime identity, baseline focused checks, minimal generation census access, and actual performance/request baseline. Register owner and exact fixture. | None |
| SC-01 | Schema-wide CodeableConcept conformance inventory, independent denominator and literal fixtures. Prove omissions in current emitter without yet hiding anything. | SC-00 |
| SC-02 | Error-aware coverage contribution, persistence, replay and complete-build contract in existing ingestion/backfill. | SC-01 |
| SC-03 | Contextual bounded presentation API for root/selected occurrence; preserve legacy absence fallback and candidate eligibility. | SC-02 |
| SC-04 | Semantic-first picker grouping, Source fields access, cross-tab/search reachability, cancellation, honest cardinality. | SC-03 |
| SC-05 | Refresh the known stale single-action driver, then first Specimen lifecycle in Basic and real CDA; reviewed coherent integration and retained evidence. | SC-04 |
| SC-06 | Condition and Medication family correctness, labels and multi-coding representation policy with tests; only demonstrated gaps change code. | SC-05 |
| SC-07 | Measured values, repeated components, units and choice arms. Keep ambiguous/versioned cases explicit unless a bounded compiler fix is justified. | SC-05 |
| SC-08 | Identifier, nested extensions, native enums, unknown structures and remaining census dispositions. | SC-05 |
| SC-09 | Related-source and row-operation context parity across `ConceptCatalog` and `RelatedFieldEditor`, plus stale/auth context regression. | SC-06, SC-07, SC-08 |
| SC-10 | Final generation coverage report, saved raw/legacy compatibility, bounded publication/viewer/export check, rollout and final acceptance. | SC-09, SC-12 |
| SC-11 | Strict census dispatch at schema-declared Resource boundaries; typed ancestry and bounded processing, with unchanged ordinary emitter behavior. | SC-01 |
| SC-12 | Typed Resource subtype narrowing through existing resolver, binding, candidate, lowering and saved identities; correlated execution and lifecycle proof before semantic grouping. | SC-05, SC-11 |

SC-01 fixture preparation can run independently of a backend owner once the census type contract is agreed. During SC-02 through SC-05, other teams may prepare bounded SC-06/07/08 fixtures and diagnoses in separate worktrees. They must not duplicate the current issue's owner or merge substantial new semantics before the first priority lifecycle closes. Share inventory/schema/contract files through one assigned owner. Do not split two workers across the same semantic rule file. The Luna max lead reviews and integrates; Sol pass/fails the exact submission. Use at most three teams under AGENTS.md, and only fill useful nonoverlapping roles.

Do not ask Sol low to choose an unresolved semantic interpretation. Return to Astra with the exact failing source fixture and two concrete options if a scalar CodeableConcept anchor cannot be justified, equivalent coding representations need a new selection policy, coverage requires changing saved binding meaning, schema metadata cannot classify a material population, or a proposed fix introduces new compiler semantics. Routine additive types, indexed queries, fixtures, UI wiring and conservative fallback follow this plan without another design round.

Stop conditions keep this bounded:

- Timebox SC-00/SC-01 discovery to 90 minutes before reporting the concrete missing evidence. Do not repair GitNexus, infrastructure or every historical test while looking for the first category witness.
- After two focused correction cycles on the same failure, preserve the reproducer and ask the same owner/lead to identify the disputed assumption. Do not add a parallel diagnosis owner.
- If coverage work grows into a new query engine, full terminology normalization, or a new unbounded request path, stop that design and return to Astra. Keep raw fallback usable.
- If the first proof requires unsupported structured-value projection, choose the supported Specimen/category witness first and retain the unsupported path. Do not expand scope to unpivot.
- A required browser case that fails stays the current integration priority. Apply the verifier's first-failure and repeated-harness-failure discipline. Keep harness time separate from product fixes; do not weaken the assertion or silently change the case.
- A running process is polled to completion. A timeout is evidence to diagnose, not permission to declare the workflow unsupported or raise the latency budget.

### Source envelopes: measured storage correction

The exact retained Specimen witness emitted 3 inventory documents / 3,440 compact JSON bytes, but the first coverage encoding emitted 44 documents / 57,707 bytes: one source, six paths and 37 facets. Facets were persisted both inside path aggregates and as separate documents. These are serialization measurements, not physical storage or index measurements.

Replace that unshipped layout with one bounded atomic source envelope holding identity, counts, completeness/truncation state, diagnostics and canonical path aggregates with nested facets exactly once. The replacement key includes project/generation/build/storage-format/schema/source-kind/resource/physical locator, but excludes auth path: auth is validated document data and changing it replaces the previous source authorization. FHIR logical IDs are not physical source keys. Do not persist raw clinical values or ephemeral occurrence evidence.

Use existing batch replacement with document-count and encoded-byte bounds; retain the 1 MiB document limit and use an 8 MiB protective batch byte cap. Reject oversized envelopes explicitly; do not silently truncate proof or revive multi-document sources. Stream a bounded cursor over envelopes, count each source once, feed paths to existing aggregation, and fold overlapping nested facets within each source before summary accumulation. Resource certificates remain the publication authority. Delete obsolete source/path/facet delete-before-write, sentinel and orphan-join machinery and tests that only enforce that removed representation. An explicit storage-format version binds envelopes/builds/certificates; old formats cannot contribute. Preserve semantic binding identities.

Use only indexes demonstrated necessary for resource envelope scans and bounded summary/partition lookup. Prove actual replacement, replay, changed-path removal, authorization drift, distinct physical keys, failed/partial batches, source facet count correctness, mixed Resource signatures, bounds and format/identity rejection in Arango. Re-encode the same witness and a bounded varied sample before replay; measure documents/bytes, maximum size, source reads, writes, materialization memory and summary counts. No full CDA replay until this gate passes.

Loom-owned top-level payload metadata (`project_id`, `auth_resource_path`, `dataset_generation`) is excluded from the schema census using the existing `isLoomMetadataField` rule. Do not mutate the original payload, suppress unknown FHIR members, or apply that exclusion to nested keys. The first real witness exposed injected `project_id` as the only unknown-member diagnostic; root-metadata and nested/other-unknown regressions are required.

### Resource-scoped completeness: verified source boundary

The real CDA manifest declares 23 resource collections; a metadata-only existence check found 15 present and eight missing. Missing collections are not empty collections. The global coverage audit must remain PARTIAL while verified resources can establish their own evidence.

SC-02 adds a resource completion record to the existing coverage collection, keyed by exact build identity and resource type, with BUILDING/MISSING/FAILED/COMPLETE states. COMPLETE certifies an existing collection, terminal project/generation-filtered enumeration, independent expected source locators and authorization partitions matching persisted source contributions, and fully published resource summaries. Existing partition completeness separately guards census/reconciliation quality. A missing collection has unknown counts; an existing zero-row collection can have a complete zero-source certificate.

Rebuild only invalidates its resource before changing rows; publish the resource certificate last. On resume, bounded retained key/auth metadata enumeration may reconstruct the full expected source set; suffix counts and existing inventory zero counts are not proof. Report this additional read cost. Legacy completed builds must not manufacture certificates from a total count.

Lookup reads global marker, requested resource certificates, authorized partitions and exact facet summaries in one bounded AQL snapshot. Global COMPLETE or PARTIAL allows candidate evaluation, but each grouped candidate needs its own complete resource certificate and every applicable authorized partition. Restricted callers may use a complete authorized partition despite incomplete unauthorized partitions; unrestricted callers must pass all populated partitions. Missing/stale/wrong identities, failed scans, absent sentinels, unexpected source locators and unfinished materialization retain raw choices. The contextual API consumes resource-backed candidate results under PARTIAL; current-table eligibility remains a separate requirement.

Required proofs include complete Specimen plus missing Task; missing versus existing-empty; exact source-set/auth partition mismatch; restricted versus unrestricted quality; resumed enumeration; rebuild/read consistency; old markers without certificates; and real CDA Specimen grouping while the eight missing roots remain explicit in the audit. This corrects source scope rather than weakening completeness.

### Embedded resources: bounded Astra ruling

A schema-declared abstract `Resource` boundary needs exact runtime subtype resolution. SC-11 uses `WalkMembersWithResourceTypes` with the generated resource registry, preserves outer source identity and concrete owner indices, and carries the full typed Resource boundary chain. Aggregate by canonical path plus boundary signature. Missing, malformed, abstract or unsupported discriminators remain explicit incomplete evidence. Unknown objects do not dispatch merely because they contain `resourceType`. Processing limits fail closed. Existing `WalkMembers` and semantic emission retain their behavior.

The schema-template inventory records Resource boundaries as parameterized expansion points rather than recursively expanding every possible nesting. Bundle is absent from the current generated resource registry; synthetic fixtures do not establish Bundle support.

SC-12 is required for the approved all-supported-schema objective, but does not block the first Specimen lifecycle. Add an ordered typed `ResourceTypeNarrowing { Path, ResourceType }` chain through existing path resolution, correlated bindings, candidate proof, physical lowering and persisted/signed source identity. Each boundary must select an exact supported type and apply its predicate inside that parent traversal before accessing children. Reject missing, conflicting or unrelated narrowings. Preserve empty-chain legacy identities. Projection and filters must exclude sibling subtypes that share path/code spelling and preserve owner correlation, multiplicity, units, nulls and fallback. Verify nested boundaries, authorization and reload before enabling semantic emission or grouping. Until then, resolved embedded concepts are counted but not executable; do not turn census completeness into semantic execution completeness.

## 9. Performance, rollout and compatibility

Measure the current same-source/source-switch/tab/search/add flow before changing it. Record request counts by endpoint, returned bytes, route pages, compiler proof invocations, server work, and native action-to-render timing. Use the same generation, source, output shape and machine for the comparison. Do not report a speedup without a baseline or compare warm results with a cold baseline.

Structural budgets are enforceable immediately: zero resource scans in picker requests; zero proposal/preview calls to establish population; zero automatic route-page walks for coverage; bounded candidate/replacement batches; reuse identical route proofs; at most one active presentation query per exact picker context; cancellation and stale-response rejection. User actions retain the verifier's five-second action-to-render requirement. Additional endpoint budgets must come from SC-00 measurements and documented existing limits, not invented latency targets. A measured miss is fixed or reported, not excused by “background indexing.”

Roll out in this order: backend understands additive contracts with grouping off; coverage build/audit runs for the generation; inspect classified/unclassified/source counts; enable grouping only for proven contexts; then expand supported contexts through SC-06 to SC-09. Prefer proof presence as the gate over a permanent configuration system. Missing, old, failed or partial proof retains raw automatically. The existing backfill command is `arango-fhir-proto backfill-semantic-inventory --project PROJECT --generation ID`; inspect its current flags and owned target before a later run. Extend this command/report for coverage rather than invent a parallel backfill CLI. A read-only audit mode may be added there if needed for SC-00; it must actually avoid bootstrap/write/lease paths and report that it did not publish proof.

The immutable generation and census completion identify data evidence. Scope/snapshot identify authorized evidence. Output/draft/selected occurrence/stage identify application proof. Search and page navigation identify display requests only. Keep these invalidations separate. Browse never mutates the draft. Cache old results for their original identity if useful, but never apply them to the next table context.

Protect filters, groups, pivots, related-source projections and source expansion by preserving physical selector and reduction semantics. Preserve contributor anchors and source lineage through row operations. Coverage metadata must not rotate route/graph IDs unnecessarily. New labels must not silently rename saved output columns or exports. Old publications stay pinned to their generation, recipe and output identity. A fallback/binding change needs a new explicit supported source identity or migration with user-visible meaning, not a reinterpretation of a saved raw column.

## 10. Sol startup and completion checklist

At startup, read this plan, its TSV, AGENTS.md and `regression-guard:verify-change`. The regression skill controls the implementation before/after bracket; this plan-only turn did not invoke a behavior-change verification run. Confirm `realpath`, branch and HEAD before writing. Preserve unrelated changes and create distinct worker/lead worktrees. Keep the accepted runtime checkout frozen during native verification. Do not merge into a watched checkout until the freeze ends. Update GitNexus after source changes before relying on it if the index is available; report its known limitation otherwise.

Use the current agreed integration branch, `feature/construction-workspace`, unless the parent/user explicitly changes it. A lead submits the exact diff/commit, tested source identity, commands and results, skipped checks, lifecycle report and remaining coverage reasons. Sol accepts or returns concrete corrections to that lead. The lead merges accepted work serially after verifying the target identity. Shared API generation, registry entries, worklist and coverage reports have one merge owner.

Completion requires schema-wide CodeableConcept discovery conformance with no resource/path allowlist, the first and subsequent selected native lifecycles to pass against the promoted source, every populated census occurrence classified, zero unclassified omissions, and explicit counts/reasons for retained or unsupported patterns. It also requires proof that missing/stale coverage restores raw choices, exact raw source fields remain selectable, old raw drafts keep their meaning, and publication/export compatibility has the stated evidence. Commit each verified coherent unit before integrating the next substantial one. Preserve final artifacts and reports outside `/tmp` according to the parent’s delivery plan.

Report elapsed diagnosis, implementation, checks, browser time and review separately where measured. Report closed user workflows and remaining unsupported data separately from test infrastructure overhead. A clean build, a script, a commit, inventory state `complete`, or a selected assertion alone is not completion.

Deferred follow-ups remain explicit: unpivot/list reshape and its duplicate selector; richer terminology equivalence selection; metadata projections not expressible by current compiler bindings; cross-generation semantic migrations; automatic unit conversion; broader contextual route-search optimization beyond what the bounded presentation request needs. None may silently become a dependency for the first semantic-first Specimen lifecycle.

### Current implementation speed check, October 16

The user narrowed the performance request to the new implementation only. Do not rebuild or benchmark the removed storage layout. The historical encoding measurements above remain historical evidence. Measure the current production emitter, envelope writes, resource materialization, finalization and indexed lookup on 1,000 explicitly synthetic sources drawn from existing Specimen, Observation and Medication fixtures. Verify independent input counts, persisted source counts, expected lookup results and zero errors. Separate setup from timed work and identify the exact tested source. One sanity run is sufficient unless it reveals slowness or suspicious results; profile the measured bottleneck before changing the design. This fixture check does not establish full CDA throughput or browser latency. Native acceptance remains required.

Private integration a7b95d7171db8122eb7bc912a17ab34e7fd5d0cb contains the reviewed backend, UI, atomic envelope and resource certificates. Affected Go packages passed. Real Arango certificate/replay/authorization checks passed on identical adapter/ingest blobs at 603e1c6a4. Live promotion and native acceptance remain pending.

### User direction: deploy to CDA, stop performance checks

The user canceled further speed checks after the synthetic sanity run and asked whether the changes were deployed and testable on CDA. Stop additional synthetic and CDA benchmarks. Preserve existing evidence without a production-scale performance claim. Proceed with the existing full, scoped, side-by-side derived inventory backfill for loom_dev_cda_fhir / cda-fhir-v1, then promote reviewed source and run the required native CDA lifecycle. The performance preflight gate above is superseded by this explicit direction. Do not mutate the source dataset or saved user Explorer.

### Active ownership after the ingestion optimization request

SC-02 availability/normal-ingestion correction remains the integration priority. Backend lead owns ingest/backfill/server/bootstrap wiring and combined integration; sc_census_schema owns catalog identity/emitter restoration; sc_coverage_persistence reviews the combined unit. UI lead holds the native Basic/CDA acceptance slot. Patterns lead owns a separate read-only trace of residual normal-ingestion shape planning, serialization, schema traversal, hashing and batch/catalog writes, excluding the already-owned coverage removal. Its deliverable is source-proven redundant work, exact callsites, required behavior and bounded proposed patches. No shared edits, runtime reads, benchmarks or SC12 emission changes are authorized by that assignment. Implementation follows closure of the current unit.

## Separate construction workflow evidence, October 18

A direct-root Group can now use an observed scalar or Coding field from one canonical connected route, then add an exact related scalar RowValue from the saved Group contributors. The registered real-CDA workflow passed 10/10 required checks and 12 report assertions on `loom_dev_cda_fhir` / `cda-fhir-v1`; source/API freeze passed and the picker/applied-grid screenshots are retained with run `M6k7Sv`. This result is construction-workflow evidence only. It does not satisfy schema-wide semantic coverage, completeness, or rollout gates in SC-00 through SC-12.

The real-CDA full-scope `COUNT_ROWS` query still exceeds the existing 8-second/256-MiB cap: the one root-reuse candidate run was killed at 8,111 ms and returned no rows or execution counters. No performance pass or speedup is claimed. The current-source Coding proposal regression `TestConstructionProposalCurrentCodingChoiceReconcilesAndPreviewsHTTP` also remains a separate known failure on base `d9b7253a2`, requiring exact CURRENT_ITEM source-projection identity. The native screenshot exposed a label follow-up: Patient.id and Specimen.id both render as “Logical id of this artifact.”
