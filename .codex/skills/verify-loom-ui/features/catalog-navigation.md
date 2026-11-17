# Inspect the external Builder catalog boundary

The external Calypr deployment is not managed in this repository. Initial
Builder loading records catalog snapshot/generation and node/edge/candidate
counts. It does not automate graph expansion, relationship selection, field
selection, or suggestion navigation.

Relevant local source includes `BuilderWorkspace.tsx`,
`components/GuidedGraphWorkspace.tsx`, `components/RouteExtensionPanel.tsx`, and
`components/ColumnSelector.tsx` under
`ui/packages/loom-ui/src/features/ExplorerBuilder/`. Match deployed versions
before using local selectors as evidence about the external application.

Catalog and suggestion snapshot tokens belong to a generation and caller's
authorization scope. Do not reuse them across sessions. Follow
[the skill](../SKILL.md) for the read-only load check and retained diagnostics;
report navigation as unverified until a scoped browser case asserts it.
