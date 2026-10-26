# Catalog navigation

## Sub-features

Resource nodes, relationships, selectable fields, repeated routes, and suggestion loading.

## How to get to it (user POV)

Load Builder and expand the resource tree from an eligible row root.

## Source entry points

- `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:345-388` loads suggestions for the selected occurrence.
- `ui/packages/loom-ui/src/features/ExplorerBuilder/components/GuidedGraphWorkspace.tsx:473-489` renders the orphan toggle, and `:571-607` renders the current traversal strip.
- `ui/packages/loom-ui/src/features/ExplorerBuilder/components/RouteExtensionPanel.tsx:59-177` starts a query and adds or replaces a relationship.
- `ui/packages/loom-ui/src/features/ExplorerBuilder/components/ColumnSelector.tsx:406-466` renders candidate field controls, and `:883-899` renders search and status.

## Driving it with Chrome CDP

Builder load currently records catalog counts. The current traversal strip, orphan toggle, relationship selector, and candidate field controls expose stable accessible labels. Graph nodes and edges still lack stable test IDs, so graph expansion remains manual.

## Gotchas

Catalog and suggestion snapshot tokens are scoped to the active generation and caller authorization. Do not reuse them across sessions.
