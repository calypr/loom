# FHIR semantic foundation

Revision 2. This replaces the handwritten datatype-union proposal for F00-02 onward. F00-01's generated index and owner-preserving walker remain accepted. The new generic compiler operation is not implemented or accepted merely because this document describes it.

The decision followed two independent designs, a compiler trace, a schema audit, executable reference probes, and one Sol cross-judgment on 2026-09-19. The comparison artifacts are in the local research directory `/private/tmp/loom-fhir-design-20260919`. No language replacement is justified by those probes.

## User outcome

A bioinformatician searches observed codes, concepts, and identifier namespaces, then adds only the features they want. Loom shows the source fields, relationship evidence, available output members, actual value types, and construction choices. The normal interface does not expose a list of every possible generated column.

Known FHIR associations work automatically. An Identifier's system and value remain paired wherever the datatype occurs. An ordinary Observation component's code and result do not require a new user mapping. The user chooses routes, outputs, reductions, and time or unit policies. They can inspect how each feature is made.

Any supported resource can be the row root. Files constrain the starting population independently. The advanced traversal graph remains available.

## Design decision

Model a feature's construction as an owner-scoped projection, not a second copy of FHIR's datatype hierarchy.

The schema owns structure. Versioned artifacts own known associations. Authoring owns user intent. The existing dataframe compiler owns query semantics, execution, and provenance.

The runtime needs one generic operation that filters an owner and projects a record of related members. It must preserve that record until the user selects an output policy. It must not extract unrelated arrays of codes, values, and units and then zip them.

For example, one component projection has this logical construction:

~~~text
Scope       component[]
Match       one code.coding[] has the selected system, version policy, and code
Record      that component's typed value, unit members, choice arm, and absence
Policy      preserve records or apply an explicitly selected reduction
Provenance  source resource revision and exact component/member coordinates
~~~

A code match inside one component cannot read another component's value. Two matching components remain two records until the selected policy resolves multiplicity. A reduction selects or aggregates records before related members are emitted as columns.

Use the published SQL-on-FHIR 2.0 projection semantics and test corpus as references. Keep ViewDefinition as a candidate import/export adapter, not Loom's native feature contract yet. It does not define Loom's graph routes, ML policies, or full contributor lineage. Accept only a pinned, validated expression subset. Never interpolate FHIRPath into AQL.

## Sources of FHIR knowledge

| Concern | Authority | Required behavior |
| --- | --- | --- |
| Fields, types, choices, and repetition | Generated schema and release-pinned StructureDefinitions | Resolve paths from the installed type graph, not resource-name switches. |
| Datatype-local associations | Release-keyed declarations of base-FHIR datatype semantics | Apply automatically wherever the datatype occurs, including unusual field names. |
| Cross-element associations | Applicable SearchParameter composites and reviewed normative base/profile declarations | Evaluate each relative selector in its declared owner and retain the source/version of the evidence. |
| Terminology meaning | Applicable CodeSystem, ValueSet, ConceptMap, and dataset evidence | Preserve recorded system/version/code. Do not infer equivalence from display text. |
| Feature construction | Saved user intent | Record selected outputs, route, reduction, time window, and unit policy. |
| Compiler support | Checked lowering capabilities | Report an engineering limitation rather than a user mapping task. |

FHIR SearchParameter composites provide useful machine-readable owner-relative associations. They are not a universal key/result ontology. Component order does not prove clinical roles, and expressions must validate against the installed schema. Equivalent aliases must not duplicate catalog contributions or inflate counts.

The official R5 bundle examined here has 26 composites among 1,239 parameters. Its component quantity/concept composites do not cover CDA's string/integer component arms. A separate reviewed declaration, sourced to the base Observation definitions, covers the logical code/value[x] association. It is not a silent widening of a SearchParameter's type filter. No per-dataset manual mapping is required for that standard association.

Missing profile evidence does not make base FHIR structure ambiguous. Truly unspecified custom relationships remain visible and may require user intent. Data is not discarded because a profile or interpretation is unavailable.

Sources: [FHIR R5 datatypes](https://hl7.org/fhir/R5/datatypes.html), [Observation element definitions](https://hl7.org/fhir/R5/observation-definitions.html), [SearchParameter](https://hl7.org/fhir/R5/searchparameter.html), [StructureDefinition](https://hl7.org/fhir/R5/structuredefinition.html), and [SQL-on-FHIR ViewDefinition 2.0](https://sql-on-fhir.org/ig/2.0.0/StructureDefinition-ViewDefinition.html).

## Structural boundary

The checked-in graph schema contains 136 definitions and 23 concrete resource roots. It is a product subset, not the entire FHIR release. The current index also lacks full profile slicing, fixed/pattern values, exact element cardinalities, canonical profile identity, and invariant expressions.

Preserve the useful generated graph. Add pinned official metadata at its generation boundary where needed, without inventing canonical URLs for internal helper definitions. Do not claim complete R5 validation, complete profile resolution, or support for missing resource types.

Unknown raw fields/resources remain retained with explicit discovery/support diagnostics. A schema-known unfamiliar datatype must not require a new Go feature variant. Datatype-specific presentation or transformation requires separate evidence; generic structural visibility does not invent clinical interpretation.

## Authored intent and identity

The authored feature refers to a versioned source binding, a row resource, an optional route, selected output members, and explicit policies. It pins the schema/package and interpretation revisions used to validate that binding.

Discovery facts and authored policy remain separate. Observed data quality does not mutate a saved feature. A changed label does not change feature identity. Concrete source array indexes belong to a dataset revision's lineage, not the stable identity of a catalog concept.

Each result retains its owner coordinates, exact choice arm, and all members needed to interpret the selected output. Coding keeps system/version/code together. Quantity keeps magnitude, comparator, display unit, unit system, and unit code together. Range retains both bounds; Ratio retains numerator and denominator. Primitive metadata survives without the primitive value. Extension ancestry includes every parent URL and the terminal value.

Numeric aggregation over differing units requires a checked unit policy. No default selects FIRST, CodeableConcept.text, Range.low, or Ratio.numerator. Lack of text is not lack of a coded result.

## Package ownership and required changes

`cmd/generate` owns projection of pinned structural artifacts into generated metadata. `internal/fhir/schema` owns immutable structure and checked selectors. `internal/fhir/semantic` owns schema-guided observation and evidenced associations. It does not depend on catalog, authoring, or dataframe execution.

`internal/catalog` aggregates bounded authorized observations into its existing generation-scoped inventory. Keep its materialized paging index. New artifact aliases must not multiply semantic counts.

`internal/explorer/authoringv2` owns saved feature intent and concurrency/idempotency. `internal/explorer/compilation` converts that intent into the existing recipe model. The generic owner-record operation belongs in the existing dataframe expression/recipe, checked semantic plan, physical IR, and renderer. Do not build a parallel evaluator.

The compiler investigation found specific gaps that this operation must close:

- Ordinary expression cardinality collapses distinct repeated axes into `Many`.
- Correlated validation accepts a unit selector, but lowering drops it before rendering.
- Correlated Coding identity does not preserve version.
- Correlated output lookup cannot currently provide complete cell-trace contributors.

Value execution and trace must consume the same bound plan. The existence of the current cell-trace endpoint is not proof that it explains correlated outputs.

Keep authorization, dataset generations, graph routes, draft concurrency, AQL execution, ClickHouse publication, and the local hot-reload workflow. Generate Go and TypeScript boundary contracts for feature intent and results, not a parallel FHIR datatype union. The UI renders server-provided construction and capability data.

Migrate persisted drafts with an explicit versioned read migration after the generic operation proves equivalent behavior. Remove source-specific Identifier/Extension/Coding write and lowering branches in that migration wave. Preserve immutable publication meaning and its original evidence.

## Language decision

Go remains provisional, not mandatory. The executable JavaScript comparison proves that available FHIRPath tooling can evaluate the required selectors. It does not prove that another runtime improves Loom as a whole.

A parser/generator in another language is acceptable if it removes maintenance burden without imposing an unnecessary service. Replacing semantic planning or execution requires a working comparison of correctness, compiler fit, provenance, authorization, data transfer, deployment, performance, and the local development loop.

The inspected SQL-on-FHIR reference runner binds an R4 model and contains newer features beyond the pinned published specification. It is not a drop-in R5 engine. The successful reference probes do not establish full R5 conformance or a performance gain.

## F00: Build the generated FHIR semantic foundation

Depends on: none. Milestone: M0. Size: large.

- F00-01. Generate semantic metadata and preserve concrete repeated owners in the schema walker. Accepted at commit `1301c3b30ceb26e4e37344b61afb890bd6d64564`. This acceptance covers the installed schema subset, not full FHIR coverage.
- F00-02. Resolve artifact-backed associations and prove one generic owner-record projection through recipe checking, semantic plan, physical IR, Arango execution, and contributor trace. This is the next implementation unit.
- F00-03. Persist and compile multi-output feature intent against the proven operation. Migrate existing drafts and remove the superseded semantic write/lowering branches.
- F00-04. Generate strict Go and TypeScript contracts for feature intent, outputs, evidence, support, warnings, and decisions. Parse shared positive/negative fixtures in both languages.
- F00-05. Connect code-first catalog selection, construction inspection, explicit policies, Preview, and save/reload in Builder. Retain the advanced graph editor.
- F00-06. Drive the integrated CDA workflow through Preview, Publish, reload, Viewer, and artifact/value/lineage checks. Measure discovery coverage and latency without claiming usability has been proven by automation.

## Acceptance targets

- K-F00-a: All hostile structural fixtures preserve exact owner-local code, value, unit, choice, extension ancestry, and absence. A schema-defined unfamiliar root adds no handwritten resource switch. Unmatched or unsupported data stays visible.
- K-F00-b: The same saved feature produces identical literal values and contributor lineage through the compiler, Preview, Publish, reload, and Viewer. No implicit FIRST or independently zipped arrays are permitted.
- K-F00-c: The populated CDA browser journey passes with automated DOM evidence. Report structural coverage, unsupported lowering, data warnings, genuine mapping decisions, inventory latency, and iteration-loop duration separately. Performance comparisons use the same fixture and machine.

## Verification boundary

The next bounded proof uses repeated components with conflicting coding systems, multiple translations, different units, string/integer arms, and recorded absence. It must execute through the real query backend and return exact contributors. Include nested equal leaf Extension URLs under different parents, primitive-extension-only data, and a non-Patient root before migrating production contracts.

The reference prototypes passed seven extraction checks, an artifact-driven R5 composite pairing check, and a reviewed all-choice component association check. These are research evidence, not completion of F00-02 or a shipped frontend capability.

Run focused checks while implementing the operation. Run the full local browser workflow after the integrated backend/frontend unit. Do not rebuild the entire dataset or run the broad suite after every metadata edit. Keep the old datatype-union experiment isolated; it is not an accepted implementation.
