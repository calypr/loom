# S03 row-definition grounding

## Required behavior

S03 adds three saved row modes to the existing Builder document. Records keep one row for each row-root resource. Groups keep one row for each field-derived or explicit group and preserve every contributing member identity. Expansion keeps one row for each item in one selected repeated scope and preserves the parent resource identity and item coordinate.

The Builder must preview membership and row counts before a CAS-protected apply. Cancel and rejected proposals must not change the draft. Publication and export must reproduce the applied definition. Two independent repeated collections must remain independent unless a later explicit operation combines them.

## Existing flow

- `authoringv2.Document` owns the durable table intent. It stores the root resource, the route, an optional population selection, and columns. It has no row-definition field.
- `AssessRowChange` and `ApplyRowChange` rebase the resource root. They preserve columns and population routes through a digest-bound proposal.
- `Population` limits eligible root records through an immutable `SelectionRevision`. Selection membership is a flat set of unique resource references.
- `compilation.semanticDocument` emits a `recipe.Output`. The output currently receives a resource row grain and no `Expand` value.
- The recipe compiler already validates one repeated selector, lowers it to `SemanticUnnest`, inserts one physical `UNNEST`, and can append an explicit row identity.
- The compiler has no grouped-row semantic or physical operation.
- `RowDefinitionPanel` only selects another eligible resource occurrence. `PopulationPanel` only attaches an existing selection and checks its mapping coverage.

## Constraints

- Keep `Population` separate from row construction. Population chooses eligible source records. Row construction decides how those records become rows. Column filters remain separate from both.
- Derive field and repeated-scope choices from the schema-backed capability snapshot. Do not hardcode FHIR resource types or member names.
- The basic frontend renders backend-produced choices and sends opaque proposal or choice identities. It does not construct FHIR paths or group semantics.
- Preserve exact resource, member, and repeated-item identity through compilation, materialization, evidence, reload, and export.
- Explicit groups may overlap and may be empty. The current selection-member collection cannot encode overlap because it stores one member document for each selection and resource reference.
- Grouping by a missing field needs an explicit policy. Expansion needs explicit empty-collection behavior. Defaults must not silently discard records.
- The saved Builder document remains the only construction definition. Do not add a second dataset-design store.
- Existing persisted workspaces validate immediately after strict JSON decoding. Introduce an explicit semantics-version migration that writes the resource-row default before validation; do not let a zero-value Go struct silently stand for records in newly authored documents.
- TypeScript may validate generic Loom API DTOs, but it must not interpret FHIR source variants, paths, choice arms, cardinalities, Identifier, Extension, or coded-value structures. The backend returns generic presentation rows and opaque choice or proposal identities for basic and advanced controls.

## Design question

Choose the durable row-definition type, the ownership of explicit group memberships, and the semantic and physical compiler boundary. The design must support incremental implementation without turning a temporary API into a permanent compatibility layer.
