# Construction editors

Users configure supported construction operations and can review or cancel their changes.

## Sub-features

Source selection; Related/Expand; reshape; coded pivot; Combine; filter and proposal previews.

## How to get to it (user POV)

Open Builder, select a table and its construction stage, then choose an operation and use its supported controls.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature construction --out /tmp/loom-construction-review`.
- The actual existing suites (constructionOperations/*.unit.test.tsx, remaining constructionWorkspace tests, and testSupport adapter tests) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: No general TLA proof. The ownership feature separately protects proposal completion.

## Gotchas

Mocks isolate the operation UI from the compiler/server. Adapter tests are not proof that every generated operation or backend result is valid.
