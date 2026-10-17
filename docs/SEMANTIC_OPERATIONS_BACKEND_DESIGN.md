# Semantic Group and Pivot backend design

Status: proposed design, not implemented. Source baseline: `75f780866bc2c69594156e44089b9b773db15699`, 2026-10-17. Product implementation and index migrations require a separate approval.

## Recommendation

Make semantic fields compiler inputs to the existing typed Group and Pivot operations. Resolve the requested field against the exact input stage, preserve its typed identity and repeated-owner correlation, and lower only the selected fields. Keep extraction inside the database query and fuse it with aggregation where the compiler can prove equivalence.

Do not make a new full-generation fact index the prerequisite. The current semantic inventory is a discovery catalog. It is not a lossless row-level execution store. An optional accelerator can be added later if a real query demonstrates that existing payload and covering-index paths are insufficient.

One field picker can use this backend, but one UI does not imply one physical query plan. Direct scalar grouping, repeated categorical membership, correlated measurements, category discovery, and contributor inspection have different costs.

## Intended usage

A user selects the fields and the intended result. The backend returns the supported operations, row grain, and any decision needed for repeated values.

| Intent | Backend meaning |
| --- | --- |
| Count specimens by Patient and Specimen type | Group by the Patient reference and exact selected type identity. Count distinct bound Specimen records. Do not traverse Patient merely to group an existing reference. |
| Count current table rows by Specimen type | Count the actual input-stage row occurrences in each category membership. This can differ from a specimen count after expansion or traversal. |
| Make columns for specimen types | Discover typed category identities within the current input population. Freeze selected categories, then aggregate the explicitly chosen cell value. |
| Summarize Height | Select the exact measurement binding. Keep each value paired with its owner occurrence and unit. Apply only a compatible reducer. |
| Group after Filter or Expand | Use the field binding and grain that survive in that stage. Do not silently return to the original resource population. |

These are examples. Resource names and paths must not appear in the implementation's policy switches. Supported field roles come from generated schema metadata and existing semantic bindings.

## What the research established

The source has three separate concepts that the UI currently makes difficult to distinguish.

1. Ordinary `GROUP` and `PIVOT` consume typed scalar columns from the current construction stage.
2. `CODED_GROUP` is a direct-root inventory operation. It emits system, version, code, and distinct-source count. It is not ordinary Group with a better field label.
3. `CODED_PIVOT` uses a correlated source binding, but is direct-root-only and its category representation lacks Coding version. Its category resolver rejects versioned choices.

The source-stage restrictions protect real assumptions. Removing their guards would not add correct composition.

The inventory also has three different levels. Bounded field profiles summarize paths. Contributions retain emitted semantic observations and source-file locators. Materialized entries aggregate observations into catalog counts and representative metadata. File-ingested source locators are file plus line, not retained vertex keys. Persisted observations do not retain a stable concrete repeated-owner identity. `Population` counts emitted events, not current table rows or distinct resources.

Consequently, catalog counts cannot answer a grouped query over a selected cohort or a transformed table. The optional coverage census is useful for conformance, but must stay independent of inventory identity, normal ingestion, and field availability. That separation was an explicit October 16 correction.

## Semantic field contract

Resolve a field's meaning before choosing its physical representation.

| Field selection | Meaning to retain |
| --- | --- |
| Exact coded concept, such as the code defining a Height observation | The code identifies the measurement field. Its paired value is the measurement, not another copy of the code. |
| Coding family, such as a specimen-type attribute | Codes vary as categorical values within the selected binding, system, and exact version. |
| Primitive field | Preserve the existing typed value and presence contract. A primitive FHIR `code` is not a structured Coding. |
| Identifier | Preserve namespace and identifier value together. Identifier remains a separate datatype. |
| Quantity | Preserve the numeric value, unit identity, comparator, and source occurrence. A scalar projection is not the whole Quantity. |
| Other structured value | Offer an explicit supported facet, or report the unsupported shape. A Range lower bound must not be labeled as the complete Range. |

Use `(system, version, code)` for exact coding equality. Display is a label, never the grouping key. Absent version means versionless, not any version. Identical displays do not make codes equivalent. Do not select the first Coding or infer an ontology mapping. Text-only CodeableConcepts remain a distinct text facet.

FHIR permits multiple Coding representations within one CodeableConcept and does not assign meaning to their order. Preserve their shared concept-owner relationship. Different repeated components or observations remain separate events, even when their code and value strings match. See [FHIR R5 datatype definitions](https://hl7.org/fhir/R5/datatypes.html#CodeableConcept).

Schema-wide support means every populated coded shape in the supported generated schema receives a supported binding or an explicit unsupported disposition. It does not mean every structured datatype supports every numeric reducer. Keep raw fields available when semantic interpretation is absent or unproven.

## Proposed compiler shape

The following is a type sketch, not a new wire API or product code. Reuse existing reference and correlated-binding types wherever their invariants match.

```text
FieldRef = StageColumn(columnId)
         | NativeField(existingCompilerChoice)
         | SemanticConcept(conceptId, bindingId, ruleVersion)
         | SemanticFamily(familyId, bindingId, exactSystem, exactVersion)

FieldFacet = PrimitiveValue(type)
           | CodingIdentity
           | IdentifierIdentity
           | MeasurementValue(unitPolicy)
           | ExplicitStructuredFacet(facetId, type)

BoundField = {
  exactInputStage,
  validatedRef,
  facet,
  correlatedBinding,
  presencePolicy,
  cardinality,
  retainedSourceAuthority
}

RepeatedUse = Scalar
            | EachDistinctValuePerInputRow
            | EachOwnerOccurrence

AggregateInputGrain = StageRowMembership
                    | StageScalarValue
                    | SemanticOwnerOccurrence
                    | DistinctBoundResource

GroupAggregate = {
  operation,
  inputFacet,
  inputGrain
}

ResolvedSemanticInputs = {
  boundFields,
  inputRowGrain,
  alignmentProof,
  repeatedUse
}

resolveFields(compiledStage, authorizedReferences) -> ResolvedSemanticInputs
lowerFieldsAndOperation(resolvedInputs, existingGroupOrPivot) -> physicalIR
```

In Go, closed variants require constructors and validation that reject combinations which do not make sense. Do not implement the sketch as a bag of optional strings. Client-supplied paths, display names, sample cardinalities, and units are not compiler authority.

The logical projection pairs category key, value, presence, unit, and owner coordinates. It does not require a materialized fact table or a full-dataset JSON array. The renderer may use a lateral loop, hidden typed columns, or a proven aggregate form. Request only the facets needed by this operation and downstream consumers.

A displayed `ALL distinct` cell is insufficient authority for semantic execution. It may have discarded duplicate events, exact code identities, or pairing. Carry the required hidden facets from their original evaluation, or use an existing proven contributor binding. If neither survives, reject that semantic use at the selected stage. Never reconstruct it by reading every original root and pretending those are the current rows.

### Keep ownership in the existing modules

| Existing owner | Responsibility |
| --- | --- |
| `internal/catalog` | Observed field definitions and stable semantic selection identities. No current-row aggregate answers. |
| `internal/fhir/schema` | Generated path, datatype, and correlated-owner validation. Reuse `CorrelatedBinding` and quantity identity paths. |
| `internal/dataframe/recipe` | Durable field references, explicit repeated-value policies, and operation version. |
| Compiler lowering and IR | Infer stage legality, value facets, unit compatibility, grain, and physical projection requirements. |
| `compiler/render/aql` | Render the selected scoped relation and existing Group or Pivot. Apply only proved physical rewrites. |
| `internal/explorer/lifecycle` | Reauthorize choices, bind receipts and stages, expose capability reasons, and run the existing proposal and Apply protocol. |

There is no proposed package move, separate semantic query engine, frontend FHIR interpreter, or second persisted copy of compiler-inferred column metadata.

## Repeated values and counting

Preserve current `COUNT_ROWS`: it counts input-stage row occurrences. A source record key is not automatically a unique input-row token. Traversal and source expansion can repeat that key.

For category membership, deduplicate selected category identities within each input-row evaluation. Preserve duplicate physical input rows as separate contributions. Do not globally deduplicate `(rootKey, category)` and call the result a row count.

```text
for each physical input row occurrence:
    memberships = distinct category tuples for this row under the binding policy
    for each membership:
        contribute this input row once to that group

aggregate the contributions at the selected group keys
```

This allows a row with two categories to belong to two groups. The proposal must say that memberships can overlap. Separate owner occurrences remain available for an explicitly named occurrence metric. A distinct-resource metric uses the compiler-bound resource identity and is named separately.

One category membership can have several paired measurement occurrences. Keep those as two inputs at different grains instead of flattening all values into the count stream or reducing them to `ALL distinct`:

| Aggregate | Input grain and meaning |
| --- | --- |
| `COUNT_ROWS` | `StageRowMembership`: add one for each physical input row that belongs to this category, even when that row has several matching measurement owners. |
| `SUM`, `MIN`, `MAX`, `MEAN`, `COUNT_NON_NULL` | `StageScalarValue` for a scalar input, or `SemanticOwnerOccurrence` for a repeated measurement. Each matched owner occurrence contributes its paired value once. Compute `MEAN` from the total non-null sum and count, never by averaging per-row means. |
| Existing `COUNT_DISTINCT` | Distinct non-null input values, preserving the current Group meaning. It does not count source resources or owner occurrences. |
| Proposed `COUNT_DISTINCT_RESOURCE` | Distinct identity at an explicitly named bound resource scope. Use only a compiler-proven identity for that scope; reject the metric when that identity is unavailable. |

Make `AggregateInputGrain` part of the lowered aggregate contract whenever a semantic input can have more than one value per input row. The renderer may fuse the calculation or use exact row-local partials, but it must preserve each aggregate's declared grain. Any partial `MEAN` state must carry sum and non-null count. A partial distinct-value or resource count cannot be combined by summing local counts; it needs exact global uniqueness.

The source confirms why resource counts need their own operation. Generic Group `COUNT_ROWS` counts grouped row memberships, while `COUNT_DISTINCT` projects the selected scalar values and returns their sorted-unique length ([renderer](../internal/dataframe/compiler/render/aql/construction_group.go#L227)). Compiler stage inference marks an exact root `_key` or a hidden root-contributor set with its resource type ([identity resolver](../internal/dataframe/compiler/lower/recipe_construction.go#L1794)); Group builds that hidden set with `SORTED_UNIQUE` ([set renderer](../internal/dataframe/compiler/render/aql/construction_group.go#L192)). Those hidden identities can support a distinct-root metric only when the requested scope matches the bound resource type. A related-resource metric needs its own exact related-record identity. This is an available proof input, not an existing generic Group aggregate. `CODED_GROUP`'s separate direct-root distinct-source count must not be reused after transformed stages.

If an intermediate relation must be materialized, addressed, or inspected by occurrence, require a proven occurrence token or an explicit multiplicity representation. Root identity alone is insufficient. A per-input-row loop can preserve bag multiplicity without inventing a global row ID.

For multiple selected repeated fields, permit scalar broadcast and proven shared-owner alignment. Do not independently flatten and zip lists. Do not create a Cartesian product unless an explicit operation requests it. Unproven alignment returns a capability reason before query execution.

Within one concept owner, duplicate representations must not multiply a measurement. The same code and numeric value in two separate owner occurrences remain two measurements. `ALL distinct` is not a valid shortcut for sums, means, or occurrence counts.

## Preserve or end bindings explicitly

Derive these rules once in compiler stage inference. Capabilities and proposal validation must use the same result.

| Stage operation | Semantic propagation rule |
| --- | --- |
| Filter | Retain compatible bindings and occurrence coordinates for surviving rows. |
| Group | Retain selected semantic key identities. Aggregates are derived values. Do not relabel an aggregate as the original source measurement. |
| Pivot | Retain typed category identities and valid value metadata for the declared outputs. End bindings that the pivot collapses. |
| Expand | Retain the selected item's owner and ordinal. Other scalar values retain their existing meaning. |
| Unpivot | Retain a shared semantic facet only when all selected inputs have a proved compatible identity and unit contract. Otherwise expose the typed raw result without a false semantic binding. |
| Related rows or fields | Bind to the exact active related record or proved contributing set. Reuse authorization and route checks. |
| Derive | Retain a source binding only for a proved identity-preserving expression. Otherwise declare a derived value. |

Supporting an operation after one stage does not establish support after every stage chain. Capability rejection must distinguish erased binding, ambiguous alignment, unsupported value shape, and stale context.

### Units are part of the operation

The catalog currently records observed units as metadata, and some reshape paths lose unit identity. That metadata alone cannot authorize arithmetic.

Resolve Quantity value, unit system and code, comparator, and presence from the same owner. Numeric reducers require compatible exact unit identities or an explicitly versioned normalization rule. Unknown units are not silently unitless. Mixed units, incompatible comparators, and partial structured projections must fail clearly before a misleading result is accepted.

This design does not introduce implicit unit conversion or terminology equivalence.

## Execution strategy and performance

Use one semantic contract with several physical strategies.

| Workload | Preferred strategy and cost boundary |
| --- | --- |
| Direct scalar or categorical root Group | Read only required scope, key, owner, and value projections. Use an existing covering index when EXPLAIN proves coverage. Exact aggregates still inspect all contributing values. |
| Correlated repeated values | Evaluate key and value within the same owner scope. Fuse the selected relation with aggregation instead of first materializing all expanded rows. |
| Related values | Start from the proved selective side where semantics permit. Apply project, generation, authorization, population, and predicates before expensive document projection. Never change route meaning to save work. |
| A small page of sorted Group keys | Reuse the existing guarded group-key frontier only when its proof applies. Aggregate all contributors for those keys. |
| A few groups containing most input rows | Execute a full exact aggregate. A 25-group page does not bound its contributors. Prefer narrow or covering reads. |
| Pivot category discovery | Enumerate categories from the complete selected input stage, with an overflow witness and continuation or explicit selection. Do not use a preview sample or global inventory population. |
| Selected Pivot columns | Share the owner extraction and dispatch matching typed keys to requested outputs. Avoid repeating the same traversal for every selected column. |
| Contributor inspection | Compute or retrieve the required bounded provenance separately when possible. Do not retain every source row just to count it. Preserve exact evidence when a downstream operation actually needs it. |

The recent SourceGroup optimization illustrates the distinction. Historical full-source execution took roughly 30 seconds. Streaming reduced retained memory from about 697 MB to 40.5 MB, but still took 27.6 seconds. The guarded key frontier reduced document lookups from 1,485,010 to 9,463 for that request. Five accepted API previews had a median of 2,797 ms. These are retained measurements for one shape, not new benchmarks or general semantic operation guarantees.

For an illustrative population of 670,000 inputs with two Type groups, a 25-group preview includes both groups. It still needs all contributors unless an exact maintained aggregate applies. Catalog occurrence counts do not establish 670,000 distinct resources. The recent frontier is not a solution to this low-cardinality case.

The live Specimen collection has a composite index containing `payload.type.coding`. Its definition does not prove that the new query is covered. The first implementation must produce canonical AQL, then inspect its plan. Do not promise an index benefit from field names alone.

Existing Arango `COLLECT` count and aggregate forms should be preferred over group-row arrays when their results and error behavior agree. The documented incremental aggregate facility does not by itself remove traversal, duplicate, unit-validation, or provenance work. See [ArangoDB COLLECT](https://docs.arango.ai/arangodb/3.12/aql/high-level-operations/collect/).

### Index policy

No initial ingestion rewrite or mandatory semantic fact backfill is proposed. Keep inventory identity independent of optional coverage. Preserve raw reads when an accelerator is absent.

If a canonical real workload misses its existing budget, compare a targeted covering or projection index with a typed occurrence sidecar. Any sidecar must use retained vertex identity, concrete occurrence coordinates, typed values, presence sentinels, and exact source validity. File-plus-line locators and representative catalog examples are insufficient.

An accelerator must be versioned and complete for its declared scope before use. Partial facts cannot imply that absent values are missing. Authorization, generation, schema, binding version, and source-set identity are part of validity. Build it independently, activate atomically, and fall back to the canonical resource plan when it is unavailable.

Do not add rollups, a universal fact store, or a background aggregate service preemptively. If exact full-population work still exceeds the latency budget, make that a measured design decision. An explicit cancellable exact-result job is a possible later strategy. Increasing timeouts or returning sampled counts is not an optimization.

## Discovery, caches, and failure behavior

Separate three questions: whether a field was observed in the authorized dataset, whether its binding is legal at this stage, and which values occur in the current filtered rows. Inventory answers the first. Compiler stage inference answers the second. The selected query or category discovery answers the third.

Do not preview every possible field or traversal while building the picker. Browse observed fields in pages, filter legality from compiled metadata, and resolve only selected fields. A stage change invalidates its choices. Merely changing display layout should not repeat semantic discovery.

Reuse existing capability, choice, receipt, and proposal APIs. Cache keys must cover project, immutable generation, authorization identity, schema snapshot, output, input stage, recipe digest, binding and emitter versions, and population or membership revision where relevant. Cache category discovery only under its exact input population and key/value definition. A receipt stored after compilation is not proof of a precompile cache.

Return distinct failures for stale choice, erased binding, incompatible units, ambiguous repeated alignment, unsupported shape, incomplete category discovery, and query deadline. Preserve the request ID and safe stage context. Do not turn failed discovery into an empty successful field list.

The existing capability-conflict regression is still red. Its frontend recovery fix is separate from this design. New semantic capabilities must use the same exact draft/stage ownership discipline and must not introduce another retry loop that reuses stale authority.

## Alternatives and synthesis

Two whole-shape candidates were written from the same grounded source and reviewed against correctness, execution, integration, migration cost, and verifiability.

- **Stage-bound compiler projections:** evaluate selected typed facts at query time and reuse the current operator pipeline. This is the proposed base. It keeps ingestion unchanged, but does not eliminate full-population read cost.
- **Durable typed occurrence facts:** persist lossless facts with root and repeated-owner identity, plus optional exact rollups. This can amortize repeated extraction, but adds build, replay, deletion, scope, absence, and invalidation contracts. It is an optional measured accelerator, not the initial foundation.
- **Frontend scalarization or removing coded-source guards:** rejected. Display cells and deduplicated arrays do not preserve the necessary occurrence and code identity. Current guards protect assumptions the backend has not yet generalized.

Adopt the fact candidate's completeness, absence, and source-validity requirements if an accelerator becomes necessary. Do not graft its storage lifecycle into the initial implementation.

The independent cross-review also selected compiler-bound projections. Its most important correction was to replace global identity-based deduplication with row-local membership evaluation. Source expansion identity is optional today, so `_key` can repeat in physical input rows. The proposed counting rule above incorporates that correction.

| Review criterion | Durable facts | Compiler-bound projections |
| --- | --- | --- |
| Correctness | 3/5 | 4/5 |
| Execution | 3/5 | 3/5 |
| Integration | 2/5 | 4/5 |
| Ingestion and migration | 3/5 | 5/5 |
| Verifiability and failure behavior | 4/5 | 3/5 |

These are qualitative design-review scores, not benchmark results. The execution tie is deliberate: neither unimplemented design has measured query performance. The decision favors the simpler integration and unchanged ingestion path. The fact candidate contributes requirements for exact occurrence coordinates, missing-root sentinels, and complete source validity if an accelerator is later justified.

### Defaults proposed for approval

- Group repeated categories by each distinct typed value within each input row. Report overlapping memberships in the proposal. Offer an explicitly different occurrence measure when requested.
- Keep `COUNT_ROWS` unchanged. A distinct-resource summary binds a specific source identity and can lower to existing distinct aggregation when that proof is available.
- Keep existing Group missing/null behavior by default. Preserve richer presence information for the operators that already distinguish it. Do not silently change old results.
- Require known compatible unit identities for arithmetic. Introduce no implicit conversion. A unitless value and an unknown unit are different states.
- Enable only compiler-proved stage bindings. Reject unaligned repeated fields before preview. Do not add an automatic cross product or read original roots behind the user's selected stage.
- Retain existing category and output-width limits. Report overflow and require explicit selection rather than claiming a truncated domain is complete.

These defaults resolve the initial operation contract. They do not establish runtime support. Before enabling the feature, implementation must prove the exact field-to-stage binding and the first CDA query's physical cost. A materially different repeat, unit, or count policy needs a design amendment.

## Compatibility

Keep saved raw fields and operation definitions valid. Existing `CODED_GROUP` retains its four-column distinct-root semantics. Existing `CODED_PIVOT` retains its accepted versionless behavior. A new versioned category representation must not reinterpret an absent legacy version as a wildcard.

New semantic field use has an explicit recipe and binding version. Persist durable field facts, not ephemeral choice tokens. Inferred annotations remain compiler-owned. Avoid a read-time workspace rewrite that changes the saved digest merely by opening Builder.

Prefer common internal lowering for new semantic projections and existing operators. Migrate legacy compiler callers only when golden result, identity, missing-policy, and lineage equivalence is proved. Do not build a second general query interpreter beside the old one.

## Implementation sequence

Each unit ends with a proved workflow. No broad implementation is authorized by this document.

| Unit | Concrete change | Acceptance gate |
| --- | --- | --- |
| 1. Pin semantics | Specify exact field facets, category membership, occurrence counting, unit policy, and legacy behavior. | Independent examples distinguish coding translations from repeated events and row counts from owner counts. Approve the proposed defaults above and pin them in tests. |
| 2. Bind one direct semantic category | Resolve an existing family against direct source rows. Lower its typed identity through ordinary Group. | Fresh CDA QA table: Patient reference plus Specimen type, with explicit distinct Specimen count. Preview, Cancel, Apply, reload, edit, removal, and original-row restoration. No user draft writes. |
| 3. Prove the physical plan | Emit canonical AQL for that unit. Compare existing index coverage, scoped scans, and document fetches. | Bounded EXPLAIN, then controlled CDA execution with an independent exact oracle. Preserve existing budgets. No index migration without its own approval. Stop before broadening if the plan is unsuitable. |
| 4. Add repeated-owner and versioned category support | Carry correlated owner coordinates, exact Coding version, and declared repeated-use policy. | Alternative codings do not double a measurement. Separate equal-valued events remain separate. No cross-item pairing or accidental root-key deduplication. |
| 5. Carry bindings through stages | Add explicit propagation rules for Filter, Expand, Group keys, and supported related anchors. | Same-field operations after each supported stage match the selected stage's rows and contributors. Erased or ambiguous bindings are rejected before execution. |
| 6. Add semantic Pivot | Resolve typed category and value facets together. Share extraction and freeze complete category discovery. | Exact category identities, overflow, duplicate policies, missing values, units, and current-population scope through the full lifecycle. |
| 7. Consolidate callers | Route the unified picker and compatible legacy compiler paths through the shared field resolver. | No duplicated legality logic, saved-draft digest drift, stale capabilities, or extra confirmation box. Remove superseded internal paths only after equivalence proof. |

Unit 2 is a semantic proof, not an unconditional release promise before Unit 3. Use an isolated implementation spike so query evidence can reject the design before it becomes a broad migration.

## Required correctness matrix

- Exact coding identity across systems and versions, duplicate displays, alternative codings, repeated identical codes, `userSelected` metadata, and text-only concepts.
- Two owner items with the same code and equal values. Code, value, unit, and choice arm must stay paired. Repeated event totals must not use `ALL distinct`.
- Missing, null, empty, blank, zero, false, and global error witnesses beyond the preview page. Preserve existing Group missing/null behavior unless a new explicit policy changes it.
- Stage-row multiplicity under duplicate routes, source expansion, and later Expand. Separate `COUNT_ROWS`, category membership, occurrence counts, and distinct bound resource counts.
- Mixed aggregate grains: one input row has category `K` and two correlated Quantity owner occurrences with values 10 and 20 in one unit. Group `K` must return `COUNT_ROWS = 1`, `COUNT_NON_NULL = 2`, `SUM = 30`, and `MEAN = 15`. If the same physical row is repeated by a route with the same root `_key`, those become 2, 4, 60, and 15 while `COUNT_DISTINCT_RESOURCE` for that root remains 1 when the compiler-proven root identity is retained. Repeated Coding alternatives inside either owner must not multiply the measurement.
- Identifier namespace/value pairs and unresolved namespaces. Quantity identity, incompatible units and comparators, and structured facets whose meaning is narrower than the source object.
- Authorization, project, generation, membership revision, and changed draft/stage identity. No global catalog result may leak into a narrower current table.
- Group and Pivot preview/publication agreement, row identity, contributor identity, edit/removal restoration, and reload. Test field propagation after every newly supported stage.
- Golden compatibility for existing raw and coded operations. Native browser assertions require actual visible rows and meaningful controls, not only HTTP 200.

Use generated-schema conformance tests to enumerate the supported coded structures. An unknown or unsupported disposition remains visible in coverage results and retains raw access. The census is a correctness tool, not another mandatory ingestion pass.

## Evidence and limits

During research, three existing catalog tests passed: `TestSemanticInventoryEmitsBeyondBoundedFieldSummary`, `TestSemanticInventoryIdentitySeparatesCodingVersionButNotDisplay`, and `TestSemanticCoverageEmissionEvidencePreservesSameCodeRepeatedOwners`. They prove only those existing invariants.

GitNexus was refreshed for the source baseline and used for navigation. Consequential claims were checked in source. Runtime inspection read index definitions only. There was no new dataset query, benchmark, ingestion, index write, deployment, or product implementation.

The low-cardinality full-population query and the proposed semantic relation have not been performance-tested. Current metadata does not preserve every Quantity detail or every repeated owner across arbitrary transforms. Those are implementation gates, not capabilities this proposal claims already exist.

### Source anchors

- [Semantic observation contract](../internal/catalog/types.go), [inventory identity and storage types](../internal/catalog/semantic_inventory.go), and [entry materialization](../internal/catalog/arango/semantic_inventory.go).
- [Generated semantic extraction](../internal/catalog/semantic_schema.go), [correlated binding validation](../internal/fhir/schema/correlated.go), and [Quantity identity paths](../internal/fhir/schema/quantity.go).
- [Construction recipe and stable column references](../internal/dataframe/recipe/construction.go), [stage inference](../internal/dataframe/compiler/lower/recipe_construction.go), and [compilation receipt stages](../internal/explorer/compilation_receipt.go).
- [Coded Group lowering](../internal/dataframe/compiler/lower/recipe_construction_coded_group.go), [Coded Pivot authorization](../internal/explorer/lifecycle/construction_coded_pivot.go), and [owner-correlated Pivot rendering](../internal/dataframe/compiler/render/aql/table_reshape.go).
- [Group rendering](../internal/dataframe/compiler/render/aql/construction_group.go), [guarded SourceGroup preview](../internal/dataframe/compiler/render/aql/source_group_preview.go), and [real-Arango equivalence test](../internal/dataframe/compiler/source_group_preview_arango_test.go).
- [Known row-operation gaps](EXPLORER_BACKLOG.md), [coverage decisions](BUILDER_WORKLIST.md), and the October 16 correction in the live checkout's untracked `docs/SEMANTIC_FIELD_COVERAGE_PLAN.md`.
