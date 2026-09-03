# Architectural TODOs

## Explorer persistence follow-up

The package audit removed the lifecycle mirror interface, repository-config
read/write methods, duplicate create methods, and generic revision transition.
`lifecycle.Service` now uses `explorer.Service` directly and its implementation
is organized by query, authoring, preview, interactive publication, and
repository publication workflows.

The remaining `internal/explorer.Store` has 11 methods spanning owner drafts,
immutable receipts/revisions, atomic interactive publication, and repository
activation. Revisit it only after deciding whether these records will continue
to share one Arango transaction boundary; do not replace it mechanically with
many one-method interfaces for tests.

The old `loom_repository_explorer_configs` collection is read-only migration
input for one compatibility window. Startup restores a missing canonical
default owner from its active revision. Remove the legacy collection spec and
migration after deployed instances have crossed that window.
