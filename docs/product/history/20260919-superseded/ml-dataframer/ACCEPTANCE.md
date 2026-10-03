# Acceptance protocol for the ML dataframe workbench

Use this protocol to execute revision 3 of the [C01-C12 plan](../ML_DATAFRAMER_DELIVERY_PLAN.md). Every threshold is a target until a run records a result. The [runbook](RUNBOOK.md) defines package checkpoints and the [contracts](CONTRACTS.md) define six readiness experiments. Prototype checks are not product acceptance. Validate planning records with:

```bash
rtk proxy node scripts/validate_ml_dataframer_plan.mjs
```

That command checks plan consistency only. It cannot certify user capability or data correctness.

## Set up the right local target

1. Confirm the integration worktree, branch, source SHA, Docker source mounts, API port, UI port, and dataset generation. Run `rtk proxy make dev-doctor` with the same environment used to start the isolated stack.
2. Reuse `make dev` and the mounted Go/Vite watchers. Run `make dev-rebuild` only for dependency, toolchain, or image changes. Do not replace the canonical `loom-demo` volumes.
3. Read the current project-local `verify` skill before runtime verification. Default ports in that skill may differ from the active instance. Use doctor output, not an assumed localhost URL.
4. Use a unique owned test project for each destructive authoring journey. Ingest raw FHIR and establish authentication. Never reset the populated working project as test setup.

For the real-data run, use the existing external-fixture workflow with `LOOM_DEV_FIXTURE_DIR=/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META` and a recorded project/generation. The source is outside the integration worktree. Validate availability and manifest before ingestion. `verify-current` checks the populated Builder and watchers; it does not by itself certify a complete research workflow.

## Require user-reachable state

Start at the ordinary project entry screen. Browser setup may create a test account/session and seed source records. It may not precreate collections, graph routes, columns, interpretation revisions, check results, or published user dataframes needed by the task.

Drive accessible roles, labels, and visible controls. Browser JavaScript may interact with DOM controls; it must not call application stores, hidden component handlers, or authoring APIs directly. Do not replace a failing control with a direct backend call. Read-only API probes are encouraged to verify the effects independently.

Fault tests may inject server-side failures through isolated test configuration. Label those tests separately. Existing API-prepared UI tests remain useful component/contract checks, but are not C-package user-task acceptance.

Assert literal expected values from checked-in source fixtures. Do not derive the expected answer from the same compiler, preview, or publication under test. For larger data, compare complete counts and a deterministic independently calculated subset. Record that distinction.

## Release journeys

| ID | Starts with | DOM actions | Required evidence |
|---|---|---|---|
| J01 | 1,000 observed concepts, no selected columns | Choose rows, search/page, select three ready concepts, Add selected, switch to graph and back, preview/export/reload | All 1,000 discoverable; exactly three chosen outputs; zero mandatory construction forms or auto-generated columns; graph/catalog share saved intent and preserve existing advanced constructions |
| J02 | Same project | Browse files, select across pages/all matching, exclude, map to specimen rows, inspect/remove unmatched | Literal membership and source-to-row counts, persisted immutable selection |
| J03 | Standard pairs and known/unknown defined extensions | Browse recognized concepts, import missing definition, search enriched label/code, select/add | Correct same-owner values; known extension recognized without manual pairing; definition versions pinned; conflicts remain visible |
| J04 | Selected concept with alternative routes and multiple contributors | Inspect construction, trace a row, change route/policy, explicitly add count | 9 related / 4 code matches / 1 ordered result; literal alternate result; only requested outputs; unchanged population |
| J05 | Dated measurements with mixed units | Choose row anchor, bounded window, latest policy, unit conversion, inspect excluded contributors | Add the 2-in-window stage to J04's example; exact boundary/tie behavior and compatible conversions; future records excluded |
| J06 | Unresolved raw structure with no concept candidate or selected column | Open unresolved inventory, define pairing, preview/save, find in catalog, select/add/reuse | Mapping approval creates zero columns; explicit add works; raw values unchanged; no repeat mapping required |
| J07 | Mixed study category values | Define exact recoding, choose unknown policy, preview/apply, reuse | Expected categories, null/empty/unknown retained or explicitly handled, no row deletion |
| J08 | Authored features | Assign roles, missing indicators, category vocabulary, research/matrix representation | Correct X/y/identifier membership, stable physical columns, list-policy blocker |
| J09 | Bad record beyond preview window | Run Check, navigate away/reload, open problem, repair, recheck, publish | Full counts, actionable issue, no stale check reuse or early activation |
| J10 | Published dataframe plus newer generation | Copy, edit copy, review refresh diff, cancel/apply, recheck | Original unchanged, explicit membership/type drift, retained old artifact |
| J11 | Checked publication | Choose export scope/mode, download, load through supplied Python example | Exact typed values, schema, roles, quality, provenance, checksum, null-marker collision case |
| J12 | Open-access CDA-FHIR, no prepared user dataframe | Complete two available research tasks from project entry with distinct row types | At least one non-Patient start, real related feature, full Check, typed export, recorded manifest and independent reference subset |

J12 does not require CDA to contain every hostile case or DocumentReference. Choose actual available concepts and record why those tasks are meaningful. The synthetic fixture supplies error cases absent from the real data.

## Define the fixtures before implementing their features

Extend `testdata/devloop-fixture` with small, explicit records. Its existing population and correlated-binding literals are documented in its README. Add new cases only for missing behaviors, with expected rows alongside the source.

Required additions include 1,000 distinct concepts on one profiled field to cross the existing 512 observation bound, exact expected inventory, terminology/extension definition fixtures with provenance, same code in different systems, a missing definition that resolves after import, and an initially unpaired structure with no concept candidate. Add temporal boundaries/ties, compatible and incompatible units, exact recoding, zero/multiple contributors, null/empty/zero/false, large integers, literal null-marker text, list values, unauthorized records, and a two-generation drift case. Keep new records isolated from existing tests by explicit population/source identity.

Use a generated larger fixture for pagination, a bad record beyond preview limits, 100,000-member selection, and memory tests. Record generator parameters and seed. Do not hand-maintain a large copied dataset.

## Measure interpretation coverage without hiding unknown data

Use a checked inventory of structural binding groups keyed by resource/profile, owning scope, code system/code or extension ancestry, and value shape. A group has one recognition disposition: recognized, unresolved, conflicting, or unsupported. Keep ordinary uncoded fields visible. Do not count a definition-backed label alone as a valid extraction mapping.

Report group counts and occurrence counts separately, with the observed universe, authorization scope, dataset generation, definition-set identity, and scan completeness. Groups with no safe mapping remain in the denominator. Bounded examples are not a complete inventory. The fixture must reconcile all groups; the real-data report must disclose incomplete enumeration instead of producing a misleading coverage percentage.

Definition fixtures can be provisioned as trusted environment setup just like a FHIR schema. The import/retry user journey itself must use visible controls. No setup script may create human interpretation revisions for J06. Adding definitions or mappings must create zero user dataframe columns.

## Use risk-based checks during implementation

| Change | Inner loop | Package closure |
|---|---|---|
| Layout, labels, editor states | Focused component tests and targeted live DOM action | Package journey, keyboard/focus and narrow-width inspection |
| Go domain or compiler semantics | Changed-package tests and direct importers; literal executed case for changed extraction | Live Arango/ClickHouse case plus affected package journey |
| Wire schema or migrations | Generated contract checks and caller compile/tests | Reload old draft, stale-client/CAS cases, and affected DOM journey |
| Selection, interpretation persistence, Check, artifacts | Lifecycle/storage tests and relevant fault tests | Real database probe, authorization/generation failures, package journey |
| Shared orchestration/watcher code | Driver tests and source identity checks | `verify-full` recovery/timing plus affected journeys |

Use existing commands where applicable:

```bash
rtk proxy go test ./internal/explorer/...
rtk proxy go test ./internal/dataframe/...
rtk proxy go test ./internal/server
rtk proxy make openapi-check graphql-check dataframe-boundaries
rtk proxy npm --prefix ui test
rtk proxy npm --prefix ui run build
rtk proxy make verify-fast
```

Run narrower package/test selectors inside the edit loop. At M1-M3, run the completed milestone journeys together. At C12, run `go test ./...`, applicable race tests for changed concurrent lifecycles, generated-contract checks, UI tests/build, `verify-full`, and all release journeys once on the integrated SHA. Do not rerun the full suite per small issue.

Update the existing browser driver with selectable C journeys. A future `--journey J06` selector is a proposed implementation task, not a currently available command. Do not document it as runnable until implemented.

## Measure responsiveness and iteration speed

Use a recorded machine/Docker allocation and dataset manifest. Separate cold startup, warm interactions, and long-running operations. Record run counts, failures, median, p95 where meaningful, and memory limits.

| Metric | Target | Measurement |
|---|---|---|
| UI acknowledgment | Visible loading/saved/error feedback within 250 ms for local actions | Browser timestamps, 30 warm interactions, p95 |
| Cached source/feature search | First useful page within 2 s | Same authorized scope and warm snapshot, 30 searches, p95 |
| Small-fixture preview | Updated rows within 5 s after request | 30 warm previews, p95; assert current receipt, not stale rows |
| Long operation submission | Operation ID/status within 1 s | Source freeze, Check, artifact preparation; 20 warm submits, p95 |
| Warm development loop | Median at most 30 s | Five source-edit-to-observed-result repetitions; include Go and UI edits |
| Large selection | No complete member list transferred to browser | 100,000-member fixture, network payload and paging evidence |
| Full Check/export throughput | No unexplained repeatable median regression over 20% against equivalent existing candidate execution | Five interleaved runs of same dataset/query/policy on baseline and candidate, when comparable |
| Large-operation memory | Stay within recorded container limits and avoid linear full-row accumulation in browser/application memory | 10x input scale comparison, peak RSS and browser heap; database memory measured separately |

For new operations without a comparable baseline, record absolute elapsed time, throughput, peak memory, and limiting stage in the first owning package. Set the release budget there before optimization, with rationale. Do not fabricate an equivalent baseline or promise millions of rows complete within 30 seconds. A complete Check can run asynchronously while the editor remains usable.

A failed target is evidence to fix or an explicit release exception requiring scope review. Never rename a failure "acceptable" after seeing the measurement. Noise needs reruns under the same conditions, not a bigger arbitrary threshold.

## Separate functional proof from usability evidence

Automation proves that a known sequence works, not that a new researcher can discover it. At each package closure, inspect labels, visible choices, error recovery, focus order, scrolling, and a screenshot of the actual running page. A human is not required in the execution loop.

The optional user-study target is four of five representative bioinformaticians finishing the basic population/feature/check/export task within 15 minutes without FHIR assistance. Record completion, errors, requests for help, and time. Until tested, report "human usability unmeasured". Do not convert a scripted browser pass into that claim.

## Close a work package honestly

Write one evidence report per package containing:

- source SHA and dirty-diff hash if verification preceded commit;
- fixture manifest, project/generation, scope, machine, ports, and serving build identity;
- commands and exit codes, plus DOM journey identifier;
- each KPI ID, target, observed value, pass/fail/unmeasured, and raw evidence location;
- independent expected rows versus actual rows/artifact checksums;
- negative cases, performance results, known limitations, and replaced/deleted code;
- final review outcome.

Set a ledger package to `accepted` only when all four implementation units and its required KPIs pass and evidence exists. Every measured KPI row carries a nonempty `observation` describing actual results and an `evidence` array of repository-local evidence files; pass/fail alone is invalid. Resolve that package's readiness gates with evidence before acceptance. Preserve failed attempts and any accepted scope changes in that report. A report's existence is not proof of its contents; the foreground reviewer must read it and inspect the actual results.

The ledger validator checks IDs, source paths, dependencies, anchors, and evidence references. It does not interpret screenshots, certify tests, or turn `planned` into `accepted`. Never mark C12 complete merely because all earlier backend tasks are done.
