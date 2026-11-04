# Proposal and Apply ownership

Users review a proposal, apply it once, and retain the current editor when an obsolete response arrives.

## Sub-features

Proposal preview readiness; cancellation; replacement; duplicate and old completions; workspace identity changes.

## How to get to it (user POV)

Open a Builder construction editor, request a proposal, review its preview, then Apply or Cancel.

## Driving it with Vitest

Preconditions: installed UI dependencies; repository root; fresh output directory. No running app or token is needed for this stand-in.

- Run `node scripts/verify-builder-formal-core.mjs --phase behavior --feature ownership --out /tmp/loom-ownership-review`.
- The actual existing suites (constructionWorkspace/useConstructionLifecycle*.test.tsx) exercise production components/functions. Read every failed assertion in `behavior/vitest.json`; don't infer correctness from a rendered fixture or a suite name.
- Require executed assertions and inspect skipped names in `behavior/behavior.json`. `partial` means listed paths remain unverified.
- Formal link: BuilderLifecycle and ApplySettlementRace; model-generated ownership replay runs the actual synchronous component.

## Gotchas

Browser events and real transport are not exercised. The component must remain wired to the hook/workspace; hook tests cover a finite set of schedules.
