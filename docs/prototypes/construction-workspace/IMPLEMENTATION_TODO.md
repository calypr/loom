# Construction workspace implementation

## Throughput checkpoint

- [x] **Blocking first steps.** Ground the work on `arch/integration` at `ba882b003`; settle one canonical ordered construction contract before wiring step editors. Treat legacy fields as the source projection in construction mode, prohibit `TableShape` there, and retain one compiler path.
- [x] **Independent workstreams.** P01 authoring, P01 compiler, P01 lifecycle/API, P02 workspace, P03 discovery, P04 operations, and P08 measurement have isolated branches and worktrees. Integrate committed changes into this branch only after review and focused verification.
- [x] **Shared mutable state.** Each code-writing delegate owns a separate worktree. The root owns this integration worktree and merges contracts sequentially. The local `loom-dev` Docker project is shared; one perf/verification owner runs it at a time.
- [x] **Smallest safe decomposition.** Keep P01's authoring, compiler, and lifecycle slices separate until the data shape compiles end to end. Start P05–P07 follow-up implementation only after their shared contracts are integrated; use a single real evaluator and avoid parallel writes to its compiler.

## Delivery checks

- [ ] P01: durable steps, stable columns, composed compiler, edit/remove/reload and stale proposal behavior.
- [ ] P02: production table workspace, action panels, real step history, automatic matching preview, Apply/Cancel.
- [ ] P03: discover related sources and add signal with explicit feature forms, time windows, and output-row coverage.
- [ ] P04: scoped eligibility and feature/outcome rules using conditions, recoding, and guided/formula expressions.
- [ ] P05: wide, long, grouped, and repeated model-input representations at intermediate stages.
- [ ] P06: exact table-revision inputs and cross-engine execution; match, append, and membership remain composable steps with explicit input updates.
- [ ] P07: saved construction and ClickHouse publication agree; reload and evidence work.
- [ ] P08: real action-to-render baseline, targeted optimization, final distribution and correctness.
- [ ] P09: optional outcome/time roles and ML-readiness evidence for sparsity, row identity, leakage, and publication identity.
- [ ] Integrated focused tests and browser verification on the isolated local stack.
