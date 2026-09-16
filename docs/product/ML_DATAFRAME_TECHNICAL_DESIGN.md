# Technical design for researcher-authored dataframes

This is the proposed target architecture for the [implementation plan](ML_DATAFRAME_IMPLEMENTATION_PLAN.md). It replaces the separate wizard-adapter design in the earlier product plan. Proposed names below are not claims that the corresponding types or endpoints already exist.

The source baseline is `arch/integration` at `a921b9e5dca1d42a84a836286140fb3b4d704f3b`. Package ownership follows `docs/PACKAGE_AUDIT.csv` and the implementation checked in `/private/tmp/loom-arch-integration`. The [gap analysis](BACKEND_GAP_ANALYSIS_20260916.md) records the initial evidence.

## One authoring model and one execution path

Guided controls and the advanced graph edit the same `authoringv2.Workspace`. Each existing `Document` remains one output. Each existing `Column` remains one stable feature identity with presentation metadata. There is no parallel `DatasetDesignV1`, wizard-only compiler, or second draft store.

The existing package name `authoringv2` stays to avoid an unrelated package move. Its wire contract and `SemanticsVersion` advance explicitly when incompatible fields change. Only the new command contract is writable after migration. Versioned readers for immutable old publication artifacts are not a second authoring implementation.

```text
Guided controls / advanced graph
              |
     versioned authoring commands
              |
explorer/lifecycle ---- explorer/arango
              |          drafts, selection revisions, interpretation revisions
              |
explorer/compilation    pure intent-to-recipe translation
              |
dataframe/recipe -> semantic -> compiler/ir -> render/aql
              |
dataframe/execution    rows plus typed evaluation evidence
              |
existing receipts -> publication -> published.Reader
                                      |
                             dataset artifact export
```

### Ownership and prohibited dependencies

| Owner | Extend it with | Keep out |
| --- | --- | --- |
| `internal/explorer/authoringv2` | Population and row intent, feature sources, policies, commands, canonicalization, mutable-draft migration | Database reads, physical collection names, generated HTTP types |
| `internal/explorer` | Immutable selection and interpretation revision records, evidence identity, artifact identity, existing store contracts | Query rendering and HTTP response handling |
| `internal/explorer/lifecycle` | Authorization, CAS, resolution of immutable references, checks, tracing, publication policy, export orchestration | FHIR extraction algorithms and CSV encoding |
| `internal/explorer/arango` | Persistence and indexes for those records using existing bootstrap conventions | Interpretation decisions and feature calculation |
| `internal/catalog` and `internal/explorer/capability` | Observed concept identities, structure and completeness evidence, supported operations | User-approved mappings, claims that observed cardinality is a permanent invariant |
| `internal/fhir/schema` | Schema facts and known structural pairing candidates | Dataset-specific clinical interpretation |
| `internal/explorer/compilation` | Resolve feature intent against frozen capabilities and interpretations into one recipe | Storage access, execution, frontend-specific defaults |
| `internal/dataframe/spec`, `expression`, and `semantic` | Backend-neutral selectors, predicates, correlated bindings, types and policy validation | Imports from Explorer, HTTP, or storage adapters |
| `internal/dataframe/compiler/{ir,lower,render/aql}` | Typed membership constraints, correlated extraction, ordered reduction and evidence expressions | Raw author-supplied AQL or duplicated FHIR interpretation rules |
| `internal/dataframe/execution` | Execute the compiled plan, stream typed evidence, enforce bounds and cancellation | Human approval state or a second semantic evaluator |
| `internal/dataframe/publication` and its Arango/ClickHouse adapters | Exact execution metadata, renewable read-retention pins, cleanup coordination | User feature meaning or artifact manifest policy |
| `internal/dataframe/published` | Read one explicit materialization, stream rows, encode supplied archive members | Explorer manifest policy or resolving a moving project-current pointer between pages |
| `internal/server` | Decode requests, establish authorization context, call lifecycle, encode responses | New business logic in handlers |

The existing dataframe package-boundary check gains assertions for these directions. New files are grouped by domain, such as `selection.go` and `interpretation.go`, inside existing packages. This plan does not create a generic workflow framework or merge packages.

### Proposed application contracts

Keep the existing authoring transport envelope and route family. Advance `CurrentSemanticsVersion` from 2 to 3 at B01 and require `semanticsVersion` on mutation requests. Reject unsupported versions before applying commands. Subsequent changes advance the semantics version only when their persisted meaning changes. Do not run separate old/new writable endpoints. Old draft decoding is an explicit storage migration, not permissive request decoding.

Extend the existing `/api/v1/projects/{project}/explorers/{explorerId}` route family with these proposed operations. Paths are relative to that prefix unless noted.

| Operation | Request identity and payload | Response and failure contract |
| --- | --- | --- |
| Existing `POST /authoring/v2/commands` | Existing command replay ID, expected draft version/digest, snapshot token; new semantics version and typed source/population/row commands | Updated canonical workspace and digest; stale draft or unsupported semantics is a structured conflict |
| New `POST /selections` | Explicit resource refs or exact source revision/output plus typed filters and exclusions; idempotency key | Complete selection revision, count, digest, source identity; no usable revision on partial scan |
| New `GET /selections/{revision}` | Exact selection revision and current authorization | Header and bounded member page with revision-bound cursor; no current-pointer resolution |
| New `POST /authoring/v2/row-change` | Expected draft digest, proposed row type, explicit path choices | Non-mutating proposal with preserved/unresolved feature IDs and bounded effect preview; proposal application goes through commands |
| New project-scoped `GET/POST /interpretations` | Project authorization; for writes, exact parent digest, definition, and applicability | Immutable revision and digest; parent conflict or invalid binding is explicit. No source-data writes |
| New `POST /authoring/v2/interpretation-preview` | Draft digest, exact current/proposed interpretation revision, output and feature IDs | Candidate receipt and bounded difference report; neither live draft nor library consumers change |
| New `POST /authoring/v2/check` | Receipt ID, output ID, check-policy version and supported limits | Receipt-bound complete/incomplete/failed report; later retrieval names the report ID |
| New `POST /authoring/v2/trace` | Receipt ID, output ID, stable row key, feature key, optional bound cursor | Typed cell status and bounded contributing-record evidence |
| New `POST /artifacts` and `GET /artifacts/{id}` | Exact published revision/output, format, idempotency key; authorized retrieval | Complete artifact identity and download capability, or explicit failure; no public URL to partial staging data |

Use domain request/result types in `lifecycle/types.go` and focused domain files. Suggested signatures are `CreateSelection(ctx, SelectionRequest)`, `AssessRowChange(ctx, RowChangeRequest)`, `PreviewInterpretation(ctx, InterpretationPreviewRequest)`, `Check(ctx, CheckRequest)`, `TraceCell(ctx, TraceRequest)`, and `PrepareArtifact(ctx, ArtifactRequest)`. Their result types carry identities rather than untyped maps.

Keep `compilation.CompileWorkspace` pure. Supply a new validated `ResolvedInputs` argument containing the selection descriptor and exact resolved interpretation definitions. Library resolution is not mutable draft state. Lifecycle resolves those inputs before compilation, and the receipt retains their canonical representation with normalized intent. No database access enters compilation. Update every caller when the signature changes.

Define `ResolvedInputsDigest` over that canonical representation. Include it in `CompilationKey` and receipt identity before execution. Include semantic membership and transform inputs in the resolved recipe digest so publication reuse cannot alias different datasets. The workspace retains exact revision references; the receipt retains the resolved content. Quality reports are created afterward and reference the resulting receipt ID. Their digests never flow backward into that receipt.

Return stable reason codes through the existing lifecycle error classes. Use validation errors for malformed/unsupported definitions, conflicts for stale identities, and unavailable/incomplete states for execution limits. Generation, authorization, and identity validation precede counts or traces so error responses cannot disclose protected records.

## Separate population, rows, and features

The proposed document structure keeps four independent decisions:

```text
Document
  Population: SelectionRevisionRef
  Rows: RowDefinition
  Route: occurrence tree rooted at Rows.ResourceType
  Columns: []Column

RowDefinition
  ResourceType
  SelectionPath: explicit semantic path from row resource to selected resource
  Grain: resource | expanded
  Expansion: absent unless Grain is expanded
  Identity: compiler-derived identity descriptor

Column
  Column: stable feature key
  Label / Table / Filter / Chart
  OccurrenceID
  Source: typed FeatureSource
  Contributors: scoped Predicate
  Reduction: explicit ReductionPolicy
  Time: optional TemporalPolicy
  Units: preserve | normalize-to-approved-unit
  Missing: preserve, with reason metadata
  Role: unspecified | identifier | feature | outcome | timestamp | ignore
```

This is a contract sketch, not a struct with every field optional. Wire variants are closed tagged unions. Go decoding rejects extra payloads and invalid combinations before constructing validated values. For example, count has no scalar-value selector, and latest requires an ordering field. Existing `expression.Type` supplies logical kind and cardinality; do not create a competing type vocabulary.

Role and missingness labels describe intent. They do not impute values, approve clinical meaning, or establish freedom from leakage.

### Immutable starting collections

`SelectionRequest` has two supported input variants:

1. Explicit typed resource references, with exclusions.
2. All matching rows of one pinned published Explorer output, using its typed filter contract, with exclusions.

An output is eligible as a selection source only if the backend can map its row identity to a source resource reference. A grouped or transformed output with no such mapping returns `SELECTION_SOURCE_NOT_ADDRESSABLE`. A displayed label, CSV column name, or opaque browser row number is never accepted as a resource identifier.

For matching-output selections, lifecycle resolves the revision, output, materialization, generation, and current authorization once. It streams all matches through `published.Reader`. Pagination settings are not part of population intent. The server verifies excluded references and normalizes duplicates. Explicit unauthorized or wrong-generation references fail without revealing their existence.

The persisted `SelectionRevision` includes project, source generation, resource type, canonical selection rule, membership digest, member count, and creation authorization scope. A published-output source also includes exact source revision ID, receipt ID, execution ID, output ID, and physical-schema fingerprint in its content identity. Its member records use compound keys containing the revision and typed resource identity. A revision becomes usable only after the complete membership stream and digest have committed. Partial staging records are not a collection and cannot be attached to a draft.

Use the existing Arango adapter and bootstrap/index conventions. Stream large membership sets to storage instead of putting every ID in a workspace JSON field, a receipt, or a single AQL bind array. Enforce configured row, byte, and duration limits. An exceeded limit yields an explicit failure, never a smaller successful collection. Bounded requests are synchronous initially; this does not justify a generic job scheduler.

Reapplying a saved selection rule creates a new immutable revision. Existing selections never follow a moving published pointer. Generation or authorization changes require explicit reselection and reconcile rather than silent membership shrinkage. Authorization is rechecked for every use; possession of a revision ID grants nothing.

Header and membership reads require the current effective authorization digest to match the stored scope. Otherwise reject the read before returning counts, rules, or member identities. Do not return a stored complete count alongside a silently filtered subset. Bind pagination cursors to selection revision, generation, and effective scope. A user who needs a different scope must create a new selection.

B02 adds the shared read-retention contract to the existing publication catalog before scanning a pinned source. Use proposed `AcquireExecutionReadPin`, `RenewExecutionReadPin`, and `ReleaseExecutionReadPin` operations keyed by execution, reader owner, and expiry. These are reader pins, not the existing exclusive bundle-writer lease. Arango atomically arbitrates pin acquisition against cleanup marking. Every physical-table deletion path must honor active reader pins. Lifecycle renews while scanning, cancels on lost ownership, and releases after completion. B08 reuses this mechanism instead of introducing an export-only retention store.

### Root changes preserve intent or fail explicitly

Replace destructive `SET_TABLE_ROOT` behavior with a proposed-row-change operation and an atomic apply command. The proposal contains the previous draft digest, the candidate row definition, an explicit route rebase map, preserved feature keys, and unresolved feature references.

`ApplyCommands` remains pure. Lifecycle resolves the proposed paths and capability evidence before invoking it. An accepted change preserves the selection revision and feature identities. It cannot silently delete columns. If several paths are valid, return them for a deliberate choice. If no valid rebase exists, retain the draft and return a structured error identifying the affected features.

The lower recipe gains a typed population constraint. For resource grain, scan authorized target rows and apply a correlated existence condition along the chosen path into frozen selection membership. This semijoin preserves one row per target resource even when several selected files reach it. Do not build an unbounded list in Go and inject it into a filter. Membership lookup uses indexed, generation-scoped storage bindings resolved by execution, not physical collection names in user intent or receipts.

Report selected resources with no reachable row separately. Do not add synthetic null specimen rows to a one-row-per-specimen output. Preserve the reverse membership mapping for trace/export. Resource identity includes project, generation, resource type, and logical source identity. Expansion adds the explicit repeated-element identity and never occurs as an accidental side effect of adding a feature.

## Preserve structure when defining features

### Concept discovery is different from an interpretation

The catalog owns observed facts. A concept candidate records resource type, repeated scope, coding system and code, extension URL ancestry, value path, choice arm, logical type, observed unit, and evidence completeness where available. Display text is a label, never concept identity.

A structural binding identifies the repeated item that owns the key and value. The code system and code must match within the same Coding item, and that Coding must belong to the component whose value is selected. The compiler must not independently flatten keys and values and then zip them together.

Extend `PhysicalPivotMap`, typed predicates, and their existing semantic validation where they already express this relationship. Add a typed correlated binding only for behavior those structures cannot express. Share this representation between code filters, pivots, and interpretation rules. Do not fix each with an unrelated string selector.

Known structural bindings can be proposed automatically. They are not proof of clinical equivalence. Missing systems, mixed value arms, incompatible units, and competing bindings remain explicit. Raw data remains available as recorded even if no interpretation is selected.

### Contributor predicates do not filter the population

The document's population determines eligible rows. Each feature's contributor predicate determines which related resources or repeated items feed that feature. A document-level eligibility predicate can require a matching relationship. An optional feature still retains rows with no contributors.

Use the existing typed predicate machinery, extended with correlated code matching. Remove `WherePath`/`WhereEquals` as the competing writable aggregate-filter representation after migrating its callers. Permit repeated relationship occurrences with distinct occurrence IDs and roles. Retain depth and cycle bounds. Traversal sharing keys must include scope, direction, relationship, predicate, and selection identity so independent features cannot contaminate each other.

### Reduction and type contracts

Separate repetition inside a field from multiplicity of related resources. `INDEXED` addresses the first; it does not prove the second is singular.

Supported policies are explicit:

- `require-one` accepts zero or one qualifying value and reports ambiguity for more than one. Zero is missing, not zero-valued.
- `collect` retains a list with typed association evidence. A flattened primitive list is not called lossless when the requested association is lost.
- `distinct` removes duplicates intentionally and reports that reduction.
- `count`, `count-distinct`, `exists`, `min`, `max`, and `contains-all` reuse existing aggregation machinery. Counting resources versus extracted values is part of the definition.
- `first-ordered` requires ordering and tie policy. Old deterministic-by-resource-key FIRST behavior migrates as an explicit lossy policy, not as inferred latest.
- `latest` and `earliest` use a named timestamp and window anchor. Equal timestamps are ambiguous unless the author explicitly selects a deterministic tie policy.
- Row expansion belongs to `RowDefinition`, not to an individual feature. The initial implementation rejects unsupported expansion variants instead of flattening implicitly.

Derive emitted cardinality and shape from the checked expression and reduction. `DISTINCT_VALUES` is array-shaped. Replace the unconditional ML-ready claim with structural suitability and measured readiness reasons. Scalar shape alone never constitutes ML readiness. Preserve old published metadata as historical metadata, not as a new assessment.

### Time and units

Temporal policies identify the contributor timestamp, row or event anchor, lower and upper bounds, boundary inclusivity, and precision requirements. They are not a magic `latest` string. Invalid dates, unknown timezone/precision, and missing anchors produce reason codes. Date-only values must not silently become exact instants.

Normalization matches an approved source unit identity to a target unit with compatible dimension. Version the conversion rule with the interpretation. Preserve the original value and unit in trace evidence. Support identity and explicitly validated linear/affine conversions first; unknown conversions remain unresolved. Do not infer a conversion from display text or add a remote terminology dependency to every preview request.

Add operators to `expression`, semantic checks, physical IR, and rendering together. Do not calculate one value in Go for preview and a different expression in AQL for publication. SUM/AVG, arbitrary formulas, imputation, encoding, and model training are outside this tranche and remain explicit follow-up capabilities.

## Version interpretations without changing source FHIR

An `InterpretationRevision` is a project-scoped, immutable definition with a stable library ID, parent revision, content digest, schema/profile applicability, author, and explanation. It contains structural bindings and approved transforms. It does not contain source rows or claim that every unknown concept must match a rule.

Store interpretation revisions through `explorer/arango`. Reuse existing canonicalization and immutable-insert conventions. Do not store them as fake recipe outputs or extend the ingestion extraction-rule configuration with researcher preferences. Recipe fragments remain lower-level expression reuse, not the authoring library.

Each feature pins an exact interpretation revision or contains an inline validated definition. Library updates never modify existing drafts, receipts, or publications. Applying an update is an explicit CAS-protected command. Reconcile freezes the resolved definition and its digest into the existing normalized receipt artifact. Compilation performs no mutable library lookup.

Mapping precedence is explicit. A selected human interpretation takes precedence over a structural suggestion. Within a revision, overlapping mappings either have an explicit priority or produce `AMBIGUOUS_MAPPING`; there is no last-rule-wins behavior. Raw unrecognized concepts remain browsable. Excluding them from a particular feature is visible in that feature's evidence.

A proposed interpretation edit creates a candidate revision and a candidate receipt, leaving the live draft unchanged. A bounded comparison reports changed values, resolved/unresolved counts, and sample affected records. Full verification is required before applying claims about the whole population. Approval applies the revision to the draft, not to source FHIR and not to every dataset using the library.

## Compute rows and evidence from the same plan

`EvaluationTrace` carries the selected row key, feature key, contributing resource references, repeated-item coordinates, chosen value arm, input/output units, interpretation revision, reduction decisions, and status. Status distinguishes at least value, no matching record, missing source value, ambiguous match, invalid type, incompatible unit, and explicitly excluded input.

Introduce a typed optional evidence sink in `dataframe/execution`. The physical plan supplies the values and evidence before reductions discard information. Disabled tracing must not materialize a large provenance object for every cell. A targeted trace request reruns only the requested row and feature under the same receipt and authorization.

`QualityReport` is a separate immutable execution result keyed by receipt, output, generation, authorization scope, and check-policy version. It is not appended to an already-hashed receipt. It records scanned rows, total-known or unknown, completeness, limits, row-key integrity, feature coverage, zero/one/many counts, invalid values, and explicit exclusions. Samples never masquerade as full counts. A timeout returns incomplete status and cannot approve publication.

Lifecycle owns `Check`, `TraceCell`, and publication gating. Preview stays a bounded row sample with links to evidence. Reuse `validateReceiptRoute`, generation checks, and authorized execution validation rather than writing different security rules for each endpoint. Do not report hidden-record counts or source paths from outside the effective read scope.

During publication, aggregate quality over the same execution stream that creates the candidate materialization. Activation requires successful full validation for the pinned contract. A failed check must leave the previous active publication unchanged. Cached preflight evidence is an optimization only when every identity matches; publication still enforces required invariants.

## Export one pinned artifact

An `ArtifactRequest` names an exact published revision, output, and format. Lifecycle resolves one matching receipt and materialization, validates authorization, and retains the binding for the entire export. `published.Reader.Stream` already accepts a concrete materialization. Use it directly instead of repeatedly resolving `CurrentProjectDataset` through GraphQL.

The initial bundle contains `data.csv`, `manifest.json`, `schema.json`, `provenance.json`, `quality.json`, and `README.md`. Source membership is included as a streamed provenance member when it cannot fit in bounded metadata. Its filename and checksum are declared in the manifest. No archive member uses source resource labels as filesystem paths.

The manifest names generation, selection digest, receipt, publication, materialization, interpretation revisions, row count, feature definitions, and checksums of the other members. It does not attempt to contain its own checksum. The completed archive receives an external digest.

Lifecycle export owns Explorer identity validation, manifest construction, read-pin lifetime, temporary-artifact state, and completion policy. `published` owns the row stream and generic encoding of supplied members; it does not import Explorer domain types. Assemble to bounded temporary storage and expose a download only after completion. Cancellation, later-page failure, expired storage, or changed authorization must not expose a partial archive as complete. Start with a bounded synchronous prepare-and-download operation; require an explicit design extension before adding durable background scheduling. Artifact cleanup applies only to owned temporary artifacts and is separate from physical-table retention.

Reuse the exact-execution reader introduced by collection selection. Resolve a stored bundle execution ID and validate it against the revision, never `FindExecutionBySelector`, which can select the latest execution. Hold a materialization retention pin while preparing the artifact. Retained source-generation availability also governs later cell tracing; if it has expired, report unavailable provenance rather than tracing current data.

For CSV, preserve the distinction between empty strings and missing values using a declared, collision-checked null encoding. Array columns use a declared JSON encoding and retain array shape in `schema.json`. Do not market such columns as numeric model inputs. Parquet is a follow-up codec over the same artifact contract, not a reason to add another dataset model.

## Migrate once and remove replaced paths

| Current construct | Target action | Preservation rule |
| --- | --- | --- |
| Proposed separate `DatasetDesignV1` adapter | Do not implement it | Both UX modes use `Document` |
| Flat `ColumnSource` with unrelated optional fields | Replace writable representation with validated variants | Preserve stable column keys and presentation |
| `WherePath`/`WhereEquals` | Convert to typed contributor predicate | Preserve string-equality behavior without pretending it was system/code matching |
| Destructive root command | Replace with assessed, atomic row change | Never clear unrelated authored state |
| Default related FIRST | Record explicit legacy ordering and lossy status | Require acknowledgment before a new affected publication; do not rewrite old values |
| Independent readiness booleans | Derive structure from checked types and readiness from evidence | Never rewrite immutable historical receipts |
| Browser-built full artifact | Replace callers with pinned server artifact | Ordinary bounded table display remains GraphQL |

Before changing wire commands, inventory repository-owned callers in the UI, server-generated API bindings, config conversion CLI, repository publication, acceptance fixtures, examples, and integration tests. Update them together. Decode old mutable drafts through one migration function and persist the new form using CAS with a migration report. Repeated migration must produce the same digest. Unknown or non-equivalent constructs require explicit repair, not best-effort reinterpretation.

Keep old immutable receipts/revisions readable for viewing and exports where their stored evidence suffices. Old receipts that cannot execute under the new compiler require explicit recompilation; never silently upgrade them during Preview or Publish. A downgrade must refuse newer mutable semantics. Back up raw drafts before deployment and test restore in an isolated store. A code revert alone is not a data migration rollback.

## Design gates that remain unproven

The ownership and target semantics are chosen. The following algorithmic checks must pass in the assigned package before dependent code lands:

1. Indexed membership semijoin at sparse and dense selections. Confirm one output per target, stable mapping, and acceptable Arango plan. Compare scan-driven and membership-driven execution without changing logical semantics.
2. Correlated system/code/component/value extraction. Literal hostile fixtures must rule out cross-element pairing before exposing mapping controls.
3. Receipt-bound evidence with reductions enabled. Exact Preview, published rows, and targeted traces must agree; no second evaluator is permitted.
4. Published selection source addressability and retained-materialization reads. An unsupported output must fail explicitly rather than guess a source ID.
5. Bounded artifact completion and authorization change. Demonstrate that partial or newly unauthorized results cannot become downloadable successes.

These are implementation gates, not completed experiments. No live behavior, performance gain, or new API implementation is claimed by this design document.
