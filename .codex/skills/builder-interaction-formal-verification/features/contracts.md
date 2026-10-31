# Client and runtime contracts

Users load Builder through decoded API responses and see consistent state or a recoverable error.

## Sub-features

API request/response decoding; top-level Builder rendering; reactive resources; accepted construction contracts.

## How to get to it (user POV)

Load a project Explorer through the frontend; retry a failed request where supported.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature contracts --out /tmp/loom-contracts-review`.
- The actual existing suites (top-level src tests including api.unit.test.ts, types.unit.test.ts and react.unit.test.tsx) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: No general TLA proof. Resource ownership source mutations are separately part of the formal audit.

## Gotchas

Test-owned fetch responses do not prove deployed authentication, reverse proxy, or real backend compatibility. Live authenticated recipes remain in verify-loom-ui.
