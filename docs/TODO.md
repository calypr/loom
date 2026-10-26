# Architectural TODOs

## Keep Explorer feature logic out of `server`

Receipt compilation in `internal/server/explorer_receipt_contract.go` validates
receipts and constructs Explorer config. Capability resolution in
`internal/server/explorer_capability.go` transforms catalog evidence and builds
the authoring catalog. Move the pure transformations to their existing Explorer
domain owners, leaving `server` responsible for transport and orchestration.
Preserve the current API contracts and verify the moved behavior with focused
tests and a live Builder path.

After that boundary is clear, split
`internal/explorer/authoringv2/commands.go` and
`internal/dataframe/execution/engine.go` by responsibility within their current
packages. Do not add packages or use line count as the success criterion. Use
`docs/PACKAGE_AUDIT.csv` as an inventory and record semantic decisions as they
are verified; its current entries do not establish that every boundary is
settled.

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
