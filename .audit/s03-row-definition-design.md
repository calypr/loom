# S03 row-definition design

Status: accepted for implementation on 2026-09-20.

## Decision

Add one closed row-definition union to each `authoringv2.Document`:

```go
type RowDefinition struct {
	Kind     RowDefinitionKind `json:"kind"`
	Records  *RecordRows       `json:"records,omitempty"`
	Groups   *GroupedRows      `json:"groups,omitempty"`
	Expanded *ExpandedRows     `json:"expanded,omitempty"`
}
```

The only S03 kinds are `RECORDS`, `GROUPS`, and `EXPANDED`. Validation requires exactly one payload matching the discriminator. S03 does not persist a transformation list and does not permit group-plus-expand or more than one expansion. A later composition feature must first define Cartesian, correlated, and zip behavior plus identity and evidence rules.

Population remains separate. It selects eligible source records. The row definition converts those records into output rows. Column contributor predicates and column-only filters do not add, remove, merge, or redefine row membership.

## Durable variants

`RECORDS` preserves the current root-resource row behavior.

`GROUPS` selects either a schema-resolved scalar key or an immutable explicit-group revision. Field grouping stores durable semantic meaning, including route occurrence and field path, after the backend resolves an opaque capability choice. Repeated keys are inapplicable until the user deliberately selects an expansion. Missing keys use an explicit `ERROR`, `EXCLUDE`, or `GROUP_AS_MISSING` policy.

Explicit group membership is an immutable input revision owned beside selection revisions, not another dataset-construction document. It has separate group headers and member relations, so empty groups and overlapping membership are representable without making the Builder draft proportional to dataset size. A revision is bound to project, generation, authorization scope, root resource type, source selection revision and membership digest, group-definition digest, group-membership digest, and completion state. The saved row definition references its revision ID.

`EXPANDED` identifies one route occurrence, one schema-resolved repeated scope, and an explicit empty-collection policy. It never accepts a client-authored FHIR path or cardinality claim.

## Identity and evidence

Resource rows retain the current root identity.

Field-derived group IDs hash the row-definition version, canonical typed key encoding, and the missing-key sentinel when applicable. Empty, zero, false, and missing remain distinct. Explicit groups use the immutable revision's stable group ID. Labels and ordinals affect presentation, not identity.

Expanded row identity is the ordered tuple of parent row identity, repeated-scope identity, and item ordinal. Equal values at different positions remain different rows.

Group rows retain exact member witnesses. Expanded rows retain parent identity, scope, and ordinal. Compilation receipts include the canonical row-definition digest and resolved group revision and membership digests. Preview, publication, Viewer evidence, and exact export resolve the same identities; a count alone is not evidence of preserved membership.

## Compiler stages

1. Resolve the population to eligible root records.
2. Resolve the closed row definition against the pinned capability snapshot and immutable membership inputs.
3. Lower records directly, expansion through the existing typed `UNNEST` path with ordinal output, or grouping through a new typed semantic and physical group operation.
4. Compile column contributors and column-only filters against the resulting member or item context without changing row membership.
5. Materialize values and provenance from the same physical plan.

Every internal variant declares its accepted root context and resulting row grain. Grouping carries ordered member identities and list-valued unreduced member values until S04 applies an explicit reduction.

## API and frontend boundary

The backend generates applicable row choices from schema-backed capabilities, validates opaque choice identities, compiles a proposed definition, and returns before/after counts, completeness, example memberships, affected columns, notices, and an opaque proposal ID. Cancel performs no write. Apply accepts the proposal ID with the expected draft version and digest, revalidates its proofs, and atomically changes only the saved row definition.

TypeScript knows the Loom product modes `Records`, `Groups`, and `Expand`, generic presentation rows, membership previews, affected-column states, and opaque choice, member, and proposal IDs. It does not interpret FHIR resource variants, `Identifier`, `Extension`, coding structures, choice arms, schema paths, or cardinality. Advanced controls display backend-provided source and route descriptions and still submit opaque identities.

## Migration

Increment `CurrentSemanticsVersion`. Before current validation, migrate every older document with no row definition to an explicit `RECORDS` payload at its existing root. Preserve routes, population, columns, filters, actions, tab identities, and column identities. Current-version mutation input must include the complete union; absence is invalid. Canonical JSON always writes the selected payload. Migration is idempotent and changes the canonical digest once.

## Alternatives rejected

- An ordered transformation pipeline publishes composition before its cardinality semantics exist.
- Inline explicit membership makes optimistic draft writes scale with dataset size.
- Reusing flat selection revisions cannot represent overlapping or empty groups.
- Client-authored schema paths duplicate FHIR semantics in TypeScript and permit stale or unauthorized intent.
- Count-only grouping loses the source tuples required for evidence and reconstruction.

## Implementation sequence

1. Add the durable union, strict validation, version migration, cloning, canonicalization, and digest tests.
2. Add immutable explicit-group revisions and schema-backed resolution for field keys and repeated scopes.
3. Add resolved compilation inputs and receipt identities, then lower one expansion through existing `UNNEST` with stable item identity.
4. Add typed group lowering with exact member witnesses and filter-stage separation.
5. Add generic choice, preview, and CAS apply APIs.
6. Add generic Records, Groups, and Expand controls, starting-record selection, affected-column repair, and J03.

## Selection record

Candidate Sol was selected as the base. The pipeline candidate contributed explicit migration rules, internal row-grain validation, and hard rejection of undefined multi-expansion behavior. The Luna candidate independently agreed on the closed union, compiler ownership, exact identity, CAS preview/apply, and a FHIR-agnostic UI; its inline membership proposal was rejected for scale. The independent cross-judge scored the closed model 27.5/30 and the pipeline 22.5/30.
