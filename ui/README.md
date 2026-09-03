# Loom UI

The `@calypr/loom-ui` package is the source of truth for the portable Explorer
Builder and the standalone Viewer's initial table workflow. Calypr will embed
the package by injecting its own `fetch` implementation; the demo uses a
same-origin, no-auth client.

The standalone Viewer does not yet replace Calypr's complete Viewer. It loads
only the first 100 rows and searches that client-side subset. Pagination,
server-side search and sorting, facets, charts, downloads, cohort persistence,
shared filters, row details, and advanced cell renderers still need to move
before the Calypr implementation can be deleted.

From this directory:

```bash
npm install
npm run test
npm run build
```

Run the standalone app against a local Loom server with:

```bash
LOOM_API_URL=http://127.0.0.1:8080 npm run dev --workspace @calypr/loom-demo
```

The demo defaults to `NCPI_ACCEPTANCE`, `default`, and Builder mode. Override
these at runtime with `?project=PROGRAM%2FPROJECT&explorer=default&mode=viewer`
or with `VITE_LOOM_PROJECT`, `VITE_LOOM_EXPLORER`, and `VITE_LOOM_MODE`.
