# Tables, population and Viewer

Users select rows, columns, populations and views while keeping the expected table state.

## Sub-features

Table shape; column selection; population membership; preview formatting; Viewer rendering and serialization.

## How to get to it (user POV)

Open Builder table/population controls, or open the Explorer Viewer and choose an output.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature tables --out /tmp/loom-tables-review`.
- The actual existing suites (ExplorerBuilder/components tests, other Builder helper tests, and ExplorerViewer tests) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: No general TLA proof.

## Gotchas

jsdom does not establish layout, actual drag/drop, scrolling, browser accessibility, downloads, or published Viewer persistence.

The recovered native-browser `download` profile verifies actual production FileCell links and exact attachment URL/filename/bytes; `drag` verifies native pointer reordering and stable row IDs across a test-owned component reload. Mounted `exportPopulation.unit.test.tsx` protects complete authorized export while transient facets narrow visible rows, exact pinned identity, Cancel/reopen, and old-output handling. Their API/HTTP boundaries are controlled; publication-to-deployed-Viewer and full ZIP/backend content remain separate from these component proofs.
