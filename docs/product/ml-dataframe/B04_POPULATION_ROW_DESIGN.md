# B04 population and row design

## Problem

A saved selection currently exists beside Builder intent. It cannot constrain compilation, so choosing two files cannot yet produce one row for their shared Specimen. The design must keep selection identity immutable, let rows start at any eligible FHIR resource, deduplicate shared targets, preserve the selected-file mapping, and reject stale generation or authorization scope before a receipt exists.

## Usage

The project explorer creates a complete selection revision and opens Builder with its revision ID. A researcher creates or chooses a table, chooses the row resource independently, and attaches the starting collection. Builder stores the selection revision and a checked semantic route from the row resource to the selection resource. Preview then returns only reachable rows. A table without a population remains a normal resource-root table, so Specimen-first and other non-file workflows keep working.

## Shape

`authoringv2.Document.Population` stores `selectionRevisionId` and semantic route steps. Public commands accept catalog edge IDs, but the reducer persists resource types and relationship labels so catalog snapshot IDs do not become durable meaning.

Lifecycle resolves every referenced selection before compilation. It requires a complete header whose project, generation, and authorization scope match the exact capability snapshot. `compilation.ResolvedInputs` contains a sorted population entry per output with the selection revision, membership digest, member count, selected resource type, and checked route. This content participates in `ResolvedInputsDigest` and receipt identity.

Compilation copies the resolved population into a storage-neutral recipe population constraint. Semantic lowering turns it into a typed population plan. Physical lowering scans the chosen row resource once, follows the declared route with the existing direction-aware traversal proof, and joins terminal resource IDs to the indexed selection-members collection. It deduplicates at the root, so two selected files that point to one Specimen emit one Specimen row. The matched selected IDs are retained in the hidden `__loom_population_members` projection for later trace and export work.

Runtime bindings provide the physical selection-members collection name. Persisted authoring and recipe documents never contain Arango collection names.

Changing a populated table root is never destructive. The command either applies a checked rebase that preserves columns and population or rejects with affected references while leaving the prior draft digest unchanged. The first implementation rejects non-trivial rebases until it can prove one unique mapping.

## Synthesis decision

This design keeps row definition and population separate. Population belongs to each output because one Explorer can contain independent tables, while resolved membership belongs to the receipt because it is external immutable input. It reuses the existing semantic and physical traversal pipeline instead of introducing a second query renderer.

## Tradeoffs accepted

- We accept one explicit attachment command after selection creation in exchange for keeping project-file selection outside the FHIR graph editor.
- We accept rejecting ambiguous or non-trivial row rebases initially in exchange for never deleting configured features.
- We retain a hidden member-ID array per row in exchange for exact traceability and a larger internal result.

## Alternatives considered

Storing all selected IDs in the workspace or receipt was rejected because large selections would make CAS drafts and receipts unbounded. Resolving a current selection during every preview was rejected because it would make old receipts drift. Treating selected files as the dataframe root was rejected because it couples population to row grain and prevents one-row-per-Specimen output.

## Open questions and risks

- Does the hidden member-ID array need a configured bound before B07 exposes cell tracing?
- For paths with more than one legal route, should the project explorer provide the route hint or should Builder ask after row selection?
- Dense selections may favor membership-driven traversal rather than target scanning; the B04 performance probe must compare both without changing semantics.

## Next implementation step

Add the durable population command and receipt-resolved input, then prove an exact two-file-to-one-Specimen Preview before adding the Builder handoff panel.
