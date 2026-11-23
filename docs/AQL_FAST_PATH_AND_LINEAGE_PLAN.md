# AQL fast-path audit and lineage agreement plan

Status: Track A paused after inventory and the isolated CDA load. Harness, timing, and deletion are pending. Track B was committed separately on `aql-lineage-agreement`. Original measurements at `bb50db30b` on `internal-cleanup`.

Precondition: `docs/AQL_RENDER_REFACTOR_PLAN.md` Phases 1 to 4 and
`docs/EXECUTION_REFACTOR_PLAN.md` are merged. This plan assumes:

- `render/aql` files are named by content;
- the sub-renderers are methods on `physicalPlanRenderer`;
- callers use `Render(plan, Options)` with a sealed `Preview` type.

File and function names below are as of `bb50db30b`. Map them to current names with that
plan's Phase 2 file map and Phase 3 rename list.

## Problem

That refactor makes `render/aql` easier to read, but it barely shrinks it. About 30% of the
package (roughly 4.5k of 15.3k lines, inferred from file sizes) encodes the same operation
semantics more than once:

- **Forward.** The query that builds output rows.
- **Alternative.** Preview fast paths. These are hand-written query shapes that must return the
  same rows faster for specific previews. About 2.2k lines.
- **Inverse.** Row lineage and cell trace, which answer "which source rows produced this row or
  cell". About 2.4k lines.
  - The inverse code shares low-level helpers with the forward code: navigation (103 calls) and
    bind-variable helpers (87).
  - It writes its own stage logic. It calls the forward group-stage renderer only twice
    (measured).

There is little copy-paste, so extracting helpers will not shrink this. It shrinks only by
keeping fewer forms. This plan has two tracks:

- **Track A** measures each fast path against the generic query and deletes the ones that do
  not pay for themselves.
- **Track B** adds a check that the inverse form agrees with the forward form, so drift fails a
  test instead of shipping.

## Evidence

The fast paths arrived in three weeks, from 2026-09-27 to 2026-10-18. Their commit titles claim
speed:

- `546338666` "Speed up terminal grouped Pivot previews"
- `5b6a1077d` "Speed up grouped Pivot previews with compact category rendering"
- `5e49c26f8` "Optimize bounded grouped row-value previews"
- `b294f6b38` "Page terminal source Group previews by root keys"
- `63e330d12` "Add cost-gated semantic Group projection factoring"

Only `45728fdb8` (streaming missing-category pivot) records a timing: 4,619 ms max on the
815,261-record CDA dataset. It records no baseline, so no fast path has a measured speedup on
record.

Existing levers:

- `cmd/dataframe-profile` renders a fixture and runs it with Arango profiling. It reports cold
  and warm wall time, peak memory, full scans and a result digest. It does not select preview
  modes.
- `make acceptance-performance` covers ingestion, publication, viewer and GraphQL. It does not
  cover Builder previews.
- `docs/REAL_DATA_ACCEPTANCE.md` and `.codex/skills/verify` explain how to load the real
  datasets into an isolated, owned project.

Lineage agreement today is spot-checked by a few tests, for example
`compiler/composed_test.go:23` (`...LineageMatchesIndependentMembershipOracleAgainstArango`) and
the preview-identity assertions in `compiler/trace_test.go`.

## Track A: fast-path audit

### Inventory

| # | Path | Selected by | Introduced |
| --- | --- | --- | --- |
| 1 | Streaming missing-category pivot preview | `compiler/execution.go:98,100` (`canRenderStreamingMissingCategoryPivotPreview`) | `45728fdb8` |
| 2 | Two-scan pivot preview | `compiler/execution.go:104` | `546338666` |
| 3 | Dynamic category pivot preview | `compiler/execution.go:109` (`canRenderDynamicCategoryPivotPreview`) | `5b6a1077d` |
| 4 | Semantic group projection factorization, with its assessment and scope count | `compiler/projection.go`, chosen by the execution group-preview planner and cached | `63e330d12` |
| 5 | Preview root-index choice (hint, or sequential with the index disabled) | `compiler/covering.go`, chosen by the execution planner through Arango explain | earlier |
| 6 | Related group preview shape | detected inside the renderer (`sequence.go:23`) | `358c064b4` |
| 7 | Group row-value preview window | detected inside the renderer (`sequence.go:54`) | `5e49c26f8` |
| 8 | Source group preview window | detected inside the renderer (`sequence.go:64`) | `b294f6b38` |
| 9 | Related eligibility category scan and count rows | detected inside the renderer (`count.go:12,15`) | `722ad053a` |

Excluded: root-page keys and the related-expand preview limit (`compiler/paging.go`). They bound
result size, so they are a correctness and memory feature, not only speed. Category scan
terminals are also excluded, because they are distinct queries. If Phase A0 shows a listed path
also bounds memory, reclassify it the same way.

### Preparation results

| Path | Selection and bound |
| --- | --- |
| Streaming missing-category Pivot | `canRenderStreamingMissingCategoryPivotPreview` selects a terminal presence-aware Pivot. The preview output limit leaves the source scan unbounded. |
| Two-scan Pivot | `CompileRecipeOutputWithPolicy` chooses this path for a compatible covering index and group-key source paths. Both scans examine the scoped source. |
| Dynamic category Pivot | `canRenderDynamicCategoryPivotPreview` selects the eligible terminal Pivot when earlier index paths do not apply. The output limit leaves the source scan unbounded. |
| Semantic Group projection | `chooseGroupPreviewScan` selects factorization after assessment. Assessment caps include a 256 MiB query limit, a 64 MiB dictionary threshold, and a 512-row sample. |
| Root-index choice | `chooseGroupPreviewScan` chooses hinted or sequential rendering based on the available index and query assessment. |
| Related Group | `relatedGroupPreviewShapeFor` limits selected groups after constructing their contributor frontier. |
| Group row-value window | `terminalGroupRowValuePreviewWindowFor` limits selected groups while retaining all their contributor keys. |
| Source Group window | `terminalSourceGroupPreviewWindowFor` windows expansion to selected groups after scanning the source. |
| Related eligibility | `renderRelatedEligibilityCountRows` has no explicit frontier memory cap. Category discovery has a separate 257-value sentinel and belongs with bounded category discovery. |

The isolated CDA generation `loom_dev_cda_fhir/cda-fhir-v1` became reusable
in 503 seconds from the start of upload. It was loaded from 11 NDJSON files with
2,055,968 input rows and named-stream SHA-256
`437d308493840f42ae6d48afa82dad62a59f8c8e4a4c39a18fa7d1845cca6d07`.
Workload drafts still need data-shape, index, and trigger confirmation before timing.
No fast path has a keep or delete verdict.

### Decision rule (fixed before measuring)

A path stays if, on its target workload, any of these holds:

- warm p50 is at least 2× faster than the generic query, and saves at least 100 ms;
- the generic query fails, times out, or exceeds the preview memory limit, and the fast path
  does not;
- peak memory is at least 2× lower, and the generic query's peak is above 25% of the preview
  memory limit.

Its results must match the generic query (see Correctness below). A path within 20% of a
threshold is borderline and goes to the operator.

### Phases

#### A0: inventory

- For each path, record:
  - its trigger shape;
  - its selection site;
  - whether it bounds memory;
  - the tests that pin it;
  - every piece of code that exists only for it, in `render/aql`, `compiler` and `execution`
    (for example the execution group-preview planner and its cache for paths 4 and 5).
- Write the inventory into this plan. No code changes.

#### A1: build the harness

- Extend `cmd/dataframe-profile`, or add `cmd/preview-audit` beside it. It takes a preview
  request and runs it two ways, with the fast path and forced generic.
- For each way, it reports:
  - warm p50, p95 and max over N runs, plus one cold run;
  - peak memory, full scans and rows;
  - a result digest.
- How to force the generic query:
  - Paths 1 to 5 are caller-selected. Pass the generic `Options`, which needs no production
    change.
  - Paths 6 to 9 are detected inside the renderer. Use a temporary toggle applied by a script,
    the same way as the render snapshot in the earlier plan. Never commit the toggle.
- The harness also confirms which path actually ran, by checking the rendered AQL, so a run
  cannot silently measure the wrong query.
- Commit the harness and the toggle script. Any new fast path must add a harness workload in
  the same change.

#### A2: workloads

- For each path, write at least one Builder preview request that triggers it on the CDA dataset
  and one at fixture scale. Derive them from the tests that pin the path.
- Load data into an isolated owned project, following `docs/REAL_DATA_ACCEPTANCE.md`. Never
  touch the canonical `loom-demo` volumes.
- Record the requests and the dataset identity in this plan.

#### A3: measure and decide

- Before measuring, record the machine, the service versions and the CPU and memory limits.
- Alternate the run order between the fast and generic queries.
- Before trusting any number, explain what limits it. Check the Arango profile for which node
  dominates, so a cold cache or a different plan cannot pass as a speedup.
- Write a results table into this plan: per path, both timings, memory, digest match and the
  verdict (keep, delete or borderline).
- Commit the table. Borderline paths wait for operator review. Clear keeps and deletes go ahead.

#### A4: delete the losers

- One commit per deleted path. It removes:
  - the `Preview` variant or shape detector;
  - the compiler selection code;
  - any execution planner or cache code that served only that path;
  - tests that only pin the fast path.
- Check:
  - the render snapshot is identical for every other path;
  - requests that used the deleted path now render the generic query, and return the
    digest the audit recorded;
  - `make test-go`, `make test-integration`;
  - run `python3 scripts/package_audit_verify.py` if a package loses all of its code.

### Correctness

The fast and generic queries must return the same rows. Compare full result sets when the
preview order is total. Otherwise raise the limit until the whole result fits, and compare
multisets. A digest mismatch is a bug, whatever the verdict. Record it with a reproducer in
`docs/BACKLOG.md`.

## Track B: lineage and trace agreement

Track B is independent of Track A and can run in parallel. It writes only new test files and
`docs/BACKLOG.md`.

### B0: inventory

For each construction operation kind (GROUP, CODED_GROUP, PIVOT, CODED_PIVOT, UNPIVOT, DERIVE,
FILTER, EXPAND, COMBINE, RELATED_SOURCE, RELATED_EXPAND, RELATED_ELIGIBILITY, RELATED_FIELD),
record:

- whether row lineage supports it;
- whether cell trace supports it;
- which tests already compare the inverse result with the forward result.

### B1: properties

Check these properties on real Arango, with `testkit/arangotest` fixtures. Run each kind on its
own and in one composed chain.

- **Completeness.** Every forward output row resolves to lineage, and every traced cell
  resolves to a trace.
- **Membership.** Every contributor exists and satisfies the operation's own rule. That means
  the group key, the pivot category, the filter predicate, or a related route that reaches the
  anchor.
- **Partition.** For keyed GROUP and PIVOT, the contributor sets of distinct output rows do not
  overlap, and together they cover the input rows.
- **Reconstruction.** Recomputing a cell from its trace contributions gives the cell's value,
  for SUM, COUNT, MIN, MAX, first-value and recode.

### B2: commit

- Commit the suite for kinds where every property holds.
- For a kind where a property fails, reproduce it, record the reproducer in `docs/BACKLOG.md`,
  and do not commit a skipped or expected-failure test.
- Report each disagreement as a bug. This plan does not fix lineage.

The separate Track B branch has commits `5740f94bd` and `6dd3ba96c`.
Owned-Arango tests passed for Group and Pivot lineage completeness, membership and
partitioning, Pivot SUM reconstruction, Pivot–Derive–Filter trace, Unpivot trace,
and source Filter trace. The runner requires the new tests. No disagreement was
reproduced. Other supported operation shapes retain the existing spot checks.

## Deferred: deriving lineage from the forward query

Starting from scratch, each operation would have one forward rendering. That rendering could
optionally carry contributor identities, and lineage and trace would read them. That would
remove the inverse form.

Revisit this only when one of these holds:

- Track B finds disagreements in two or more kinds;
- the roadmap adds construction kinds faster than the inverse form can be maintained.

Then prototype carrying contributors on GROUP. Measure its cost with the Track A harness against
today's inverse lineage, and decide from the numbers.

## Ownership

- Track A writes in `render/aql`, `compiler` and `execution`, plus `cmd` for the harness.
- Track B writes new test files and `docs/BACKLOG.md`.
- Each track runs in its own worktree.
- A4 is the only phase that deletes code. It runs serially, one path at a time.

## Out of scope

- Changing any surviving path's AQL.
- New fast paths.
- The execution group-preview cache, except where A4 deletes the path it serves.
- `render/clickhouse`.

## Expected result

Track A deletes the fast paths that do not clear the rule. How many is unknown until A3; at most
it removes about 2.2k renderer lines plus their selection and planner code. Every path that
stays has a measured reason on record, and the harness makes new ones prove themselves.

Track B shrinks nothing. It turns silent drift between the forward and inverse forms into
failing tests, and it supplies the evidence for the deferred decision.

## Risks

- **The workloads may not trigger a path the way users do.** The harness checks the rendered
  AQL to confirm which path ran.
- **Timings are noisy on a shared machine.** Alternate the order, repeat suspected results in
  reverse order, and explain each number from the profile before acting on it.
- **Some fast paths may hide a correctness difference** that the existing tests accept, for
  example row choice under a non-total order. The full-result comparison exposes it. Treat it as
  a bug, not as a reason to keep or delete the path.
