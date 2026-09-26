# Construction workspace implementation

## Throughput checkpoint

- [x] **Blocking first steps.** Ground the work on `arch/integration` at `ba882b003`; settle one canonical ordered construction contract before wiring step editors. Treat legacy fields as the source projection in construction mode, prohibit `TableShape` there, and retain one compiler path.
- [x] **Independent workstreams.** P01 authoring, P01 compiler, P01 lifecycle/API, P02 workspace, P03 discovery, P04 operations, and P08 measurement have isolated branches and worktrees. Integrate committed changes into this branch only after review and focused verification.
- [x] **Shared mutable state.** Each code-writing delegate owns a separate worktree. The root owns this integration worktree and merges contracts sequentially. The local `loom-dev` Docker project is shared; one perf/verification owner runs it at a time.
- [x] **Smallest safe decomposition.** Keep P01's authoring, compiler, and lifecycle slices separate until the data shape compiles end to end. Start P05–P07 follow-up implementation only after their shared contracts are integrated; use a single real evaluator and avoid parallel writes to its compiler.

## Delivery checks

Checkpoint A progress (2026-09-25): a generic `RELATED_SOURCE` step can add an
`ALL_MATCHES` list at the current row stage. The local Patient-to-Observation
path passed proposal preview, Apply, reopen, ClickHouse publication, GraphQL
row comparison, and public per-cell contributor trace. Publication also produced
a complete, output-row-scoped sparse report: 2 output rows, 2 nonempty lists,
3 list entries, and 1 row with multiple entries. A separate Arango oracle
passed the no-match case and typed root-key paging. The local browser
verification suite passed. These checks cover one field form and one published
fixture; they do not close Checkpoint A or the held-out F0 constructions.

The saved related-source step now opens an editor in the live builder. Focused
tests cover route replacement, stable step and output IDs, downstream input
references, proposal preview, and Apply. The integrated browser checks found
the saved step, opened Edit, and confirmed the three intended creation actions.
Preview now labels whether its row limit sampled the output. The same
compiler-proved related route can produce an all-values list, a distinct
source-record count, or a presence flag. A live Arango test checked zero
matches, duplicate graph paths, the output types, and contributor traces for
the new forms. The route choice now advertises supported contributor
conditions. A selected scalar field can filter its own related records by
presence or an exact string or code value before list, count, or presence
output; the live Arango oracle checks filtered values and traces. Focused
builder tests checked count and exact-value authoring, and the local browser
journey passed after integration. Guided group and expand editors now cover
their saved policies. GROUP now saves how absent or null group keys are
handled: one missing group, row exclusion, or an error. A live Arango oracle
checked all three outcomes and preserved whole-table summaries. Output-row
coverage before Apply, conditions on other
fields or repeated elements, time windows, and the remaining held-out
constructions are open.
The 50-choice route request took about 1.1 seconds on the local fixture; P08
still needs representative latency measurement and improvement.

Related-record path expansion now has a guided Reshape editor and a dedicated
AQL stage. The live Arango oracle passed duplicate paths, a filtered input
stage, stable identities, and all three no-match policies. The synthetic local
API returned a ready three-row preview, and a disposable Explorer applied,
reopened, and published the step. ClickHouse returned the same three
Patient–Observation ID pairs with distinct row IDs. The exact-record Add
columns source now uses stage-bound scalar choices. A saved Observation
`status` field reopened and published the same three rows and values to
ClickHouse; another field proposed from that result retained the row count.
The browser showed the exact-record source, applicable preview, and editable
saved step. The Arango oracle covered FILTER-preserved terminal identity and
scoped lookup. Onward traversal from the terminal record, contributor trace,
expansion predicates in the UI, repeated-field policies, and same-proposal
predecessor creation remain open; small-fixture timings do not close P08.

- [ ] Close the [full plan audit](PLAN_AUDIT.md) against the current source, not the earlier `arch/integration` inventory. No listed failure case can remain only as prose without an owner and an executable or browser check.
- [x] Checkpoint A: stage-local related source, sparse contributor evidence, exact preview, save, ClickHouse publication, and editable reopening on one real path.
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
