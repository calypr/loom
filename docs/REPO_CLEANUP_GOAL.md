# Repository cleanup goal

## Product boundary

The product is the frontend UI and backend API. Test infrastructure exists to
protect those surfaces and may be deleted when it becomes slower or harder to
maintain than the behavior it checks.

## Required verification contract

- No individual test, model check, mutation control, generator check, or
  verifier may run longer than 30 seconds.
- All required jobs must fit within one 10-minute CI window. Independent work
  runs in parallel and large suites use deterministic shards.
- Browser acceptance is outside required CI. Historical browser reports are
  records, not current-source proof.
- The complete required suite is timed only at coherent checkpoints; editing
  does not repeatedly invoke the full suite.

## Current design

`.github/workflows/tests.yaml` splits UI static checks, four Vitest shards,
four Go groups, ClickHouse tests, four Node shards, Python modules, shell
checks, generated contracts, and the UI build into independent jobs.

The Builder formal core contains three bounded statecharts:

- `BuilderLifecycle.tla` checks proposal/preview/Apply ownership, settlement,
  request cancellation/generation safety, and discovery ownership.
- `ApplySettlementRace.tla` checks attempt-bound completion, outstanding callbacks,
  and current-context ownership after cancellation or an identity change.
- `ReconcileLifecycle.tla` checks current request and generation ownership,
  receipt snapshot/output matching, stale refresh, bounded retry, and repair.

Each model maps to current production symbols and reviewed source hashes.
Each check has a 30-second subprocess limit; the audit batch is sharded in CI.
Every behavioral rule has a model control and an independent real-source proof
that fails on broken code and passes after restoration. The Apply model drives
all bounded transitions against the actual synchronous ownership component;
the broader models use mounted probes rather than complete React refinement. Concrete FHIR semantics, React
rendering, transport, persistence, and browser behavior remain implementation
test concerns.

The formal runner also mounts the real hook/workspace, lowers fresh observations
into `ImplementationConformance.tla`, and checks their safety properties.
pstack's parser/IR/Mermaid backend renders those executions using the maintained
statechart view. `ApplySettlementRace.tla` captures an outstanding-callback gap
in the coarse lifecycle model. Real cancel/reapply reproductions drove an
attempt-bound completion handle and current-context cleanup checks in production
code. Keep those replays and mutation controls as regressions; a smaller model
alone is not implementation proof. The existing formal skill owns the iterative
find/replay/fix/control loop and generated diagram.

## Cleanup decisions

- The Playwright specs, configs, package dependencies, browser runners,
  browser-only helper closure, retained browser-evidence pipeline, and slow
  acceptance workflows are retired.
- The former formal catalogue, adapters, proposal machinery, mutation runner,
  and stale TLA models are replaced by bounded models and mounted implementation checks.
- Node CI follows relative imports and schedules only tests whose dependency
  closure is present and browser-free.
- CdA/FHIR fixtures remain available to fast frontend, API, and oracle tests.

Preserve unrelated working-tree changes. Do not reset, stash, clean, commit,
push, or publish this branch as part of cleanup.
