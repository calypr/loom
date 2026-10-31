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
- Formal link: ConstructionChain checks immediate predecessor references and predecessor-column availability through the actual backend validator for a bounded two-stage FILTER or DERIVE chain. These UI tests do not themselves establish that property. Proposal ownership remains a separate model.

## Gotchas

Mocks isolate the operation UI from the compiler/server. Adapter tests are not proof that every generated operation or backend result is valid.


Actual top-level derived arithmetic has an explicit `--phase api --feature derived-policy` Arango profile and `scripts/lib/formal-derived-policy.mjs` source-control wrapper. It checks 400 source/literal policy inputs with typed errors and exact small results; eight priority cases are labeled conformance. This extends query execution coverage beyond the fixed APPEND addition witness without claiming a TLA bound or separately quantified construction-stage adapter. Saved population route matching also now respects optional edge/direction pins, with legacy unpinned behavior retained.
