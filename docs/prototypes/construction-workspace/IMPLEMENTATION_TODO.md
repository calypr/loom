# Construction workspace implementation

## Throughput checkpoint

- [x] **Blocking first steps.** Ground the work on `arch/integration` at `ba882b003`; settle one canonical ordered construction contract before wiring step editors. Treat legacy fields as the source projection in construction mode, prohibit `TableShape` there, and retain one compiler path.
- [x] **Independent workstreams.** P01 authoring, P01 compiler, P01 lifecycle/API, P02 workspace, P03 discovery, P04 operations, and P08 measurement have isolated branches and worktrees. Integrate committed changes into this branch only after review and focused verification.
- [x] **Shared mutable state.** Each code-writing delegate owns a separate worktree. The root owns this integration worktree and merges contracts sequentially. The local `loom-dev` Docker project is shared; one perf/verification owner runs it at a time.
- [x] **Smallest safe decomposition.** Keep P01's authoring, compiler, and lifecycle slices separate until the data shape compiles end to end. Start P05–P07 follow-up implementation only after their shared contracts are integrated; use a single real evaluator and avoid parallel writes to its compiler.

## Delivery checks

- [ ] Close the [full plan audit](PLAN_AUDIT.md) against the current source, not the earlier `arch/integration` inventory. No listed failure case can remain only as prose without an owner and an executable or browser check.
- [ ] Checkpoint A: stage-local related source, sparse contributor evidence, exact preview, save, ClickHouse publication, and editable reopening on one real path.
- [ ] Checkpoint B: entity, event, and category frames across held-out FHIR paths, with the promised absence, time, list, Quantity, grouping, and long-output policies.
- [ ] Checkpoint C: transformed AQL stage combined with a pinned ClickHouse artifact, followed by another operation and publication; G1/G2 input updates stay explicit.
- [ ] Checkpoint D: independent researcher task study, representative capability and preview latency, authorized evidence, and sampled/unavailable labels.
- [ ] F0: frame sparse related records into a model table using the six decisions and three end-to-end constructions in `SPARSE_RECORD_FRAMING_WP.md`; verify resource-agnostic semantics, population-scoped discovery, coverage limits, time roles, and the published data dictionary.
- [ ] P01: durable steps, stable columns, composed compiler, edit/remove/reload and stale proposal behavior.
- [ ] P02: production table workspace, action panels, real step history, automatic matching preview, Apply/Cancel.
- [ ] P03: discover related sources and add columns with explicit output forms, time windows, and output-row coverage.
- [ ] P04: scoped population and contributor selection using conditions, duplicate handling, and representative-record policies.
- [ ] P05: wide, long, grouped, and repeated representations of ArangoDB records at intermediate stages.
- [ ] P06: exact table-revision inputs and cross-engine execution; match, append, and membership remain composable steps with explicit input updates.
- [ ] P07: saved construction and ClickHouse publication agree; reload and evidence work.
- [ ] P08: real action-to-render baseline, targeted optimization, final distribution and correctness.
- [ ] P09: dataframe evidence for sparsity, row identity, source multiplicity, coverage, and publication identity.
- [ ] Integrated focused tests and browser verification on the isolated local stack.
