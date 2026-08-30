# Architectural TODOs

## Consolidate Explorer persistence boundaries

Revisit the two broad, overlapping Explorer interfaces during the Explorer
package audit:

- `internal/explorer/store.go` defines a 14-method `Store` covering repository
  configuration, explorers, drafts, compilation receipts, revisions,
  publication, and activation.
- `internal/explorer/lifecycle/types.go` defines a 12-method `Store` that
  largely mirrors `explorer.Service` to support lifecycle orchestration and
  tests.

Do not mechanically split these into more interfaces. First decide which
Explorer workflows and compatibility tracks remain, assign persistence and
workflow ownership to their packages, and then remove or narrow the interfaces
that no longer represent real external boundaries.

Completion criteria:

- Explorer persistence responsibilities have explicit owners.
- Lifecycle orchestration does not mirror the entire Explorer service solely
  for tests.
- Remaining interfaces are consumer-owned and limited to the workflows their
  consumers actually execute.
- Obsolete workflow methods, adapters, fakes, and tests are deleted together.
