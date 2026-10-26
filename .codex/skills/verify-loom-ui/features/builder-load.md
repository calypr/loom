# Builder load

## Sub-features

Authenticated project routing, V2 Builder contract loading, catalog hydration, saved workspace restoration, and explicit error recovery.

## How to get to it (user POV)

Open the project, choose Explorer, select Builder, and supply the Explorer ID in the route query.

## Source entry points

- `ui/packages/loom-ui/src/Builder.tsx:34-85` resolves the Builder identity and mounts the workspace.
- `ui/packages/loom-ui/src/api.ts:1410` loads the V2 Builder state.
- `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:3130-3147` renders the load failure and retry control.

## Driving it with Chrome CDP

Run `verify.mjs drive`. Require a valid `ExplorerBuilderState`, no authentication redirect, and no visible no-V1-fallback error. On a load failure, require the visible `Couldn’t load this dataset.` alert and enabled `Try again` control.

## Gotchas

The browser-visible `builder.json` response is Next.js page data. It can return 200 while the server-side or subsequent Loom Builder request fails.
