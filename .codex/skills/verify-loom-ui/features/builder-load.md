# Builder load

## Sub-features

Authenticated project routing, V2 Builder contract loading, catalog hydration, saved workspace restoration, and explicit error recovery.

## How to get to it (user POV)

Open the project, choose Explorer, select Builder, and supply the Explorer ID in the route query.

## Driving it with Chrome CDP

Run `verify.mjs drive`. Require a valid `ExplorerBuilderState`, no authentication redirect, and no visible no-V1-fallback error.

## Gotchas

The browser-visible `builder.json` response is Next.js page data. It can return 200 while the server-side or subsequent Loom Builder request fails.
