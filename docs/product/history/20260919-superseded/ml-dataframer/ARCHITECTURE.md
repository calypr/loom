# Architecture for the ML dataframe workbench

This is the revision-3, code-first design reference for the [C01-C12 delivery plan](../ML_DATAFRAMER_DELIVERY_PLAN.md). Existing behavior below was inspected at `3f71ece81ddf3ae6e008e67e08fc5eb57d5682bd`. Proposed components and contracts are identified explicitly. Source inspection is not evidence that a browser journey passes. Detailed identity, transaction, inventory, definition and lifecycle decisions are in [CONTRACTS.md](CONTRACTS.md); implementation checkpoints are in [RUNBOOK.md](RUNBOOK.md).

## Existing implementation and remaining gaps

| Concern | Existing owner and evidence | Gap addressed by this plan |
|---|---|---|
| Application entry | `ui/apps/demo/src/main.tsx` reads project/explorer/selection from the URL and renders Builder or Viewer | C01 adds a reachable project/dataframe entry; C02 removes precreated-selection dependence |
| Builder state | `ui/packages/loom-ui/src/Builder.tsx` accepts `selectionRevisionId`; `features/ExplorerBuilder/BuilderWorkspace.tsx` owns commands, reconciliation, and layout | C01 reorganizes the default task without creating another state owner |
| Population | `internal/explorer/selection.go`, `lifecycle/selection.go`, `lifecycle/population_report.go`; UI `PopulationPanel.tsx` | C02 adds initial record discovery, selection authoring, route explanation, and unmatched paging |
| Row meaning | `internal/explorer/lifecycle/row_change.go`; UI `RowDefinitionPanel.tsx` derives choices from existing occurrences | C01/C02 expose arbitrary eligible starting roots and assess later changes before apply |
| Feature discovery | `internal/catalog/semantic.go`, `internal/explorer/capability/domain.go`; UI `ColumnSelector.tsx` searches candidates for the selected occurrence | C01 provides a paged code/concept catalog and explicit multi-selection; C03 enriches recognized meanings |
| Feature policies | `FeaturePolicyEditor.tsx` edits field/aggregate branches; typed lookups fall through; unit presets are hardcoded | C03-C05 provide a complete editor backed by supported operations |
| Definition loading | `internal/ingest/source_schema.go` currently snapshots graph-schema metadata; `internal/fhir/schema` validates generated structural bindings | C03 adds supported version-pinned definition/terminology resolution; the inspected path is not a terminology service |
| Bounded concept observations | `internal/catalog/semantic.go` caps semantic observations per profiled field at 512 and silently skips new identities; persisted field documents do not establish artifact completeness | C01 needs a durable inventory/backfill and completeness record, not a larger array; C03 refresh cannot assume retained observations are complete |
| Correlated FHIR extraction | `internal/fhir/schema/correlated.go`, `internal/dataframe/compiler/lower/correlated_binding.go` | Reuse same-owner correlation; C06 exposes explicit human binding decisions |
| Interpretation | `internal/explorer/interpretation.go`, `lifecycle/interpretation_preview.go`; UI `authoring/interpretationCandidate.ts` requires a unique concept match | C06 starts from raw unresolved structures, including cases without any concept candidate or authored column |
| Value recoding | `InterpretationFeatureDefinition` describes source/contributor, not a typed recoding table | C07 introduces explicit category transformation in the existing feature/compiler path |
| ML intent | `internal/explorer/authoringv2/semantic_types.go` defines columns and source policies | C08 adds roles, missingness intent, and research/matrix representation |
| Quality | `internal/dataframe/publication/quality.go`, `internal/explorer/lifecycle/publish.go` enforce complete publication evidence | C09 provides a separate user-visible Check, candidate retention, and actionable diagnostics before activation |
| Explanations | `internal/explorer/lifecycle/cell_trace.go`; UI `features/ExplorerViewer/CellExplanationDialog.tsx` | C06/C09 connect interpretation decisions and draft-preview repair to the same evidence semantics |
| Artifacts | `internal/explorer/lifecycle/artifact.go`, `internal/dataframe/published/artifact.go`; UI `Viewer.tsx` | C08/C11 add ML-specific type/role metadata and an independently tested loading contract |
| Browser acceptance | `scripts/loom-dev.mjs`, `scripts/verify-population-row-ui.mjs`, `scripts/verify-row-definition-ui.mjs` | Some tests inject selection/route/column state before DOM checks; C packages need complete user-authoring journeys |

GitNexus query was used for navigation. Its first result reported two commits of staleness. The local index was refreshed successfully before the final query. The analyzer reports bounded/capped process coverage, so missing graph relationships are not evidence of absent code. The decisions above were checked in source.

## One authoring definition and one execution path

```text
Concept catalog + selected columns  Graph entry + column inspector
            \                        /
           shared authoring commands and save/reconcile hook
                              |
                   authoringv2.Workspace
                              |
           lifecycle resolves authorized pinned dependencies
                              |
               explorer/compilation -> recipe
                              |
          dataframe semantic checking -> IR/compiler -> execution
                              |
            candidate execution + complete quality evidence
                              |
                     publication activation
                              |
                  Viewer + pinned typed artifact
```

The concept catalog is a read model, not an automatically generated authoring document. Only an explicit user selection creates column intent. The new interface is another editor for existing authoring intent. It is not a new `DatasetDesign` API translated through a permanent compatibility adapter. Selections and interpretations remain separately revisioned dependencies because they already have independent reuse and identity.

Each C package changes the authoritative contract once, migrates every writer, and removes the replaced writer. Immutable old receipts keep their original meaning. Mutable drafts use a versioned, idempotent migration when needed. Unsupported old receipts return an explicit recompile/retention error instead of being interpreted with new semantics.

## Ownership and proposed component boundaries

| Owner | Responsibilities | Must not acquire |
|---|---|---|
| `ui/apps/demo` | Project routing, host integration, authenticated client, ordinary entry links | FHIR extraction or hidden API setup for a product feature |
| `ui/packages/loom-ui/src/features/ExplorerBuilder` | Proposed concept catalog, selected basket, source picker, optional construction inspector, unresolved-data review, Check views; one shared command hook | Another persisted draft store; duplicate compilation or statistics calculation |
| `ui/packages/loom-ui/src/{api,types,selection,interpretation}.ts` | Validated wire types and client requests | Untyped payload bags or user-facing business decisions |
| `internal/catalog` and `internal/explorer/capability` | Observed facts, pinned terminology enrichment, supported operations, complete paged discovery, snapshot identity | Guessing clinical equivalence or authorizing raw resource access |
| `internal/fhir/schema` | Pure interpretation of supported definition metadata and validation of same-owner bindings | HTTP fetching, terminology-server hosting, or an alternate extraction engine |
| `internal/ingest` | Existing schema-loading orchestration plus bounded trusted definition imports | Silently dereferencing URLs supplied by source records |
| `internal/explorer/authoringv2` | User intent, commands, canonicalization, migration, feature roles/representation | Database queries, job execution, UI layouts |
| `internal/explorer` | Selection/interpretation/check domain contracts and immutable identities | HTTP response logic or a second dataframe evaluator |
| `internal/explorer/lifecycle` | Authorization-bound orchestration, source browse/freeze, dependency resolution, preview/check/publish/refresh | A generic workflow framework or direct FHIR-path interpretation |
| `internal/explorer/arango` | Existing owner/revision persistence plus scoped new metadata/check records | Clinical matching heuristics or browser concerns |
| `internal/explorer/compilation` | Translate authoring intent and resolved dependencies into the existing recipe | Independent execution of transforms |
| `internal/dataframe/spec`, semantic, IR, compiler | Checked reductions, recoding, representations, temporal/unit semantics, trace lowering | Project library persistence or user approval workflow |
| `internal/dataframe/publication` | Candidate materialization, full output quality, execution identity | Deciding which UI role is a predictor |
| `internal/dataframe/published` | Typed reads, streaming artifact encoding and loading metadata supplied by its caller | Re-resolving current source or interpretation heads |
| `internal/server` and `openapi/openapi.yaml` | Authentication/transport boundary, validated request/response contracts | Matching, reduction, missingness, or recoding rules |

New UI components are cohesive files in the current feature area. Do not add C01-C12 as a giant package, feature flag branch, or another thousand lines inside `BuilderWorkspace.tsx`. Extract existing responsibilities while their callers migrate, not as a separate cleanup project.

## Catalog entries are not output columns

The catalog lists observed concepts and ordinary fields in the authorized project. A concept identity includes its coding system/code or structural identity. Candidate variants retain owning scope, value binding, source profile, and relationship identity. Equal names do not merge different meanings. Group equivalent-looking entries for navigation only; the concrete source variant remains explicit before adding.

Do not enumerate the Cartesian product of concept, route, time window, unit, and reduction as thousands of catalog rows. Fetch route alternatives and examples when a concept is selected or inspected. A source-search result does not persist a draft, create a receipt, or materialize a table.

The selected basket contains explicit user choices pinned to a capability/definition snapshot. Add selected resolves and validates the batch on the server, then uses atomic existing authoring commands. Stable column identities survive reload. Duplicate clicks are idempotent; a deliberately different output for the same concept needs an explicit action. An unresolved choice keeps the batch pending with an explanation, rather than silently skipping selected items.

Recognition and output readiness are separate. Use typed recognition results for schema-recognized, definition-recognized, human-mapped, unresolved, conflicting, and unsupported. A recognized many-valued concept may still require a reduction decision. Count evidence independently records its denominator and exact/sampled/not-computed status. Do not use one confidence score to stand in for these different facts.

## Resolve definitions before asking for manual mappings

C03 uses configured version-pinned definition sets and terminology snapshots, shared where authorized. Users do not need to establish normal code/value roles record by record. The catalog consumes pure resolved facts; metadata import performs I/O separately. Optional additional definitions can be imported through an authorized action. An unavailable definition is distinct from an ambiguous binding or an unsupported compiler capability.

The chosen initial implementation is local versioned imports, not a new online terminology server. Any configured downloader validates hosts, size/dependency limits, checksums, and FHIR-version compatibility. It must not follow arbitrary canonical URLs from ingested records or transmit dataset examples externally. Import errors do not modify the active definition set. Definitions have provenance and explicit versions/digests in capability and receipt identity.

FHIR extension definitions can supply content and meaning through StructureDefinition. LOINC's published terminology attributes describe the measurement; they are not encoded as interpretable digits in the code itself. These justify deterministic lookup and supported definition interpretation, not arbitrary clinical-equivalence inference. Sources: [FHIR extensibility](https://www.hl7.org/fhir/extensibility.html) and [LOINC term structure](https://loinc.org/kb/users-guide/major-parts-of-a-loinc-term).

Retain separate recognition provenance and observed structure. The selected numeric unit comes from the actual quantity and the checked conversion policy, not a guessed unit inferred from a label. Validate inferred bindings against existing compiler-supported ownership/type rules. Report unsupported profile constraints or unresolved modifier semantics rather than treating every imported definition as executable.

C06 is the fallback for unresolved structures. Its raw inventory exists before a concept candidate or dataframe column. Previewing a proposed mapping uses the existing compiler on bounded source examples, without requiring a previously authored feature or a second evaluator. Saving a mapping updates catalog eligibility. It creates no output column and never rewrites ingested source.

## User intent is distinct from observed evidence

Observed evidence includes code systems, sample values, resource links, distributions, units, and cardinality. User intent includes the row type, population, selected features, source binding, reduction, temporal policy, recoding, role, and output representation.

A display label is not concept identity. System/code, owning repeated scope, nested extension ancestry, source profile, and selected relationship can change a feature's meaning even when its label is unchanged. Discovery returns those identities; the UI presents the distinguishing parts before selection and retains full details behind "Source details".

Two kinds of human decisions remain separate:

- Structural interpretation chooses which key and value belong together. C06 binds to compiler-backed candidates and records applicability.
- Value recoding converts an already extracted typed value into an explicit output category. C07 records exact cases and unknown policy.

Unit conversion is neither of those. Missingness policy is also separate. This avoids a universal "mapping" object whose optional fields gradually become another programming language.

The transform order is explicit. Contributor conditions and temporal selection operate on source records. Extraction and supported reduction produce a typed value. Unit normalization precedes numeric reduction when required. Recoding then acts on that result. Representation produces the exported research value or deterministic matrix columns. Invalid combinations fail at the semantic boundary.

## Contract changes by package

These are proposed operations, not claims that endpoints already exist. Select exact route and type names against the current OpenAPI conventions during each package.

| Package | Proposed addition or redesign | Existing mechanism to retain |
|---|---|---|
| C01 | Paged observed-concept catalog, selected basket, candidate-resolution/atomic add action, reachable resource-root entry | Existing catalog facts, Explorer owner, typed column commands and CAS |
| C02 | Authorized source browse; raw-source selection variant; named collection head if absent | ResourceRef, immutable membership, published filters, population reports |
| C03 | Supported pinned definition imports, deterministic interpretation/enrichment, mapping-coverage report | Schema metadata, correlated bindings, catalog candidates and capability snapshots |
| C04 | Optional inspectable construction, short route/multiplicity decisions, explicit batch output policies | Existing column identities, trace, reduction/projection semantics and compiler |
| C05 | Supported unit capability registry; complete temporal editor contract | Temporal bounds, ordering, and normalization semantics |
| C06 | Raw unresolved inventory, bindings constructed from observed structure, mapping preview without a column, catalog publication | Interpretation revisions, same-owner compiler bindings and candidate execution |
| C07 | Closed typed recoding definition and unknown policy | Feature source/contributor definition, canonical intent and recipe compilation |
| C08 | Feature roles, explicit missingness intent, research/matrix representation | Authored column key and emitted physical-column mapping |
| C09 | Persistent check operation and candidate activation split | Receipt, materialization execution, quality accumulator and publication activation |
| C10 | Explicit copy and generation rebind/diff | Existing draft owner, immutable selection and interpretation revisions |
| C11 | Versioned role/type/loading metadata and explicit export scope | Pinned server artifact and streaming encoder |

Every added operation carries project and scope authorization at the boundary. Capability snapshot, generation, source identity, and relevant draft/receipt identity bind mutable interactions. Cancellation is explicit for potentially long source-freeze/check/export operations. UI pagination uses opaque server cursors tied to those identities.

## Full Check is candidate publication without activation

C09 splits the current lifecycle operation rather than inventing a second data path. The wire/domain states are queued, running, complete-pass, complete-fail, incomplete-failure, and canceled, as defined in CONTRACTS §8. Idle and stale are local/derived UI conditions, not extra stored operation states. Terminal evidence includes receipt, candidate execution, policy version, scope, generation, counts, and completeness. A running operation cannot have a passed verdict.

The lifecycle persists operation identity before execution. Idempotent retry resolves to that operation. A bounded worker uses existing execution infrastructure; process restart either resumes safely or records an interrupted terminal result. It must not silently rerun against changed data. C09 tests the chosen strategy and documents it before implementation.

A complete candidate can be activated only after its policy passes and current authorization/generation checks succeed. An edited draft invalidates UI check reuse. Activation never substitutes a different candidate. Failed or abandoned candidates have bounded retention and cleanup; their diagnostic summary survives long enough to explain the failure. The previous active publication is unaffected.

Do not accumulate the entire dataset in Go or React to make Check convenient. Reuse stream-based quality and database-backed paging. Report incomplete counts honestly when execution limits stop a scan.

There is an important existing distinction in `qualityAccumulator.fail`: a semantic error can establish a complete negative verdict while the row stream has not been exhausted. That report does not prove complete prevalence counts. C09 separates verdict certainty from population-scan completeness. Recoverable per-cell problems need diagnostic results lowered through the existing checked compiler path. Fatal query, infrastructure, or authorization failures stop the scan and expose partial evidence. Test that diagnostic and ordinary execution return identical good values, and that no diagnostic placeholder can pass publication policy.

## Typed implementation guidance

TypeScript uses the repository's runtime schemas and inferred types. Parse external JSON as `unknown` at the client boundary. Use branded validated IDs for draft, receipt, selection, interpretation, and operation identity where accidental interchange is possible. Derive component inputs from canonical contract types rather than copying interfaces.

Use discriminated unions for source choices, output representation, binding decisions, and asynchronous UI states. Exhaustive switches fail compilation when a new variant lacks a renderer. Avoid a bag of optional fields or `as` assertions to force wire payloads into domain types. Keep editor-local unsaved input separate from acknowledged canonical state and reject stale asynchronous responses by identity.

Go keeps business operations in their existing domain owners. Prefer concrete values and small consumer-defined interfaces at I/O seams. Use `context.Context` as the first parameter for I/O and propagate cancellation. Wrap errors with `%w`; classify with `errors.Is` or `errors.As`, not error-string matching. Closed variants have constructors/validation at the transport/domain boundary, not repeated defensive checks at every internal call.

Do not introduce a generic repository, plugin registry, service locator, transformation framework, or event bus for these packages. Shared query/compiler behavior is shared because it defines output meaning, not merely because two functions look alike.

## Compatibility and deletion rules

Coordinated breaking changes are allowed, but migrate the browser client, generated OpenAPI, CLI conversion, examples, fixtures, and direct callers in the same package. The active publication must survive failed candidate work. Preserve saved user definitions through explicit migrations or documented errors, never by silently dropping fields.

Targeted replacements include the default graph-first layout, occurrence-only feature discovery, field/aggregate-only policy rendering, singleton-only binding review, assumptions that mapping preview needs an existing column, hardcoded frontend unit choices, and competing ML export paths. The Advanced graph remains an intentional supported view. There is no repository-wide package reorganization in this plan.

## Planning limits

Sizes in the delivery plan are relative risk/effort classes, not hour estimates. Source browse, repair applicability, Check retention, and exact loader encoding need executable contract spikes inside their owning packages. Each spike ends with a decision and a test, not a new architecture-audit tranche.

No live product test was run to create this plan. Earlier B evidence remains historical. C12 must measure the current code/data combination instead of treating that historical evidence as a release pass.
