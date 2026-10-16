# Catalog navigation

## Sub-features

Resource nodes, relationships, selectable fields, repeated routes, and suggestion loading.

## How to get to it (user POV)

Load Builder and expand the resource tree from an eligible row root.

## Driving it with Chrome CDP

Builder load currently records catalog counts. Add stable frontend selectors before automating tree expansion and candidate selection.

## Gotchas

Catalog and suggestion snapshot tokens are scoped to the active generation and caller authorization. Do not reuse them across sessions.
