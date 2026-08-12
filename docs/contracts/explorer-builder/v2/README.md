# Explorer Builder semantic-concept contract v2

This contract is the boundary between Loom's data interpretation and the
Explorer Builder's researcher-facing field picker. A semantic concept is a
stable, selectable key/value definition. It may be backed by a direct leaf,
coded value, extension, observation, identifier, or a future source shape.

## Compatibility and interpretation

- `schemaVersion: 2` identifies this contract. Readers must ignore unknown
  properties so producers can add metadata.
- `resourceType`, `family`, `ruleId`, `logicalType`, `source.system`, and
  `source.kind` are strings, not closed enums. Known values may improve
  presentation, but unknown values must remain valid metadata.
- `ruleId` is a versioned producer-owned string such as
  `observation.coded-value.v3`. It identifies extraction behavior and must not
  be inferred from a display label.
- `source` records provenance: system, version, resource, key paths, value
  paths, and optional terminology. `selector` is the executable representation
  used by recipe translation.
- `column.name` is a stable generated name. It must not be regenerated from a
  UI label, and the selected concept ID/rule ID remain the audit identity.
- Suppressed examples contain no values. Clients must not reconstruct examples
  from raw rows when `examples.suppressed` is true.
- Repeated values resolve to arrays. `repetition.rowExpansion: none` means
  selecting the concept does not explode rows.
- `completeness` and diagnostics distinguish a partial scan from a valid empty
  catalog. Partial results must be disclosed to the user.

## Authoring and publication

Authoring persists concept identity, rule ID, column name, and optional label
override. Loom resolves the concept to a selector and output column during
validation/publication. Resolved output records retain concept ID, rule ID,
selector, and materialization metadata so a release remains auditable if a
later catalog scan discovers new concepts.

The fixtures intentionally include FHIR terminology, a future source system,
unknown logical/family strings, grouped families, safe example suppression,
repetition arrays, partial/truncation diagnostics, concept selections, and an
authored/resolved READY publication.
