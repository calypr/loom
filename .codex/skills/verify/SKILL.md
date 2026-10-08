---
name: verify
description: Verify Loom Builder features in the real local browser, including bounded operation composition, CDA correctness, persistence, performance, and publication.
---

# Verify Loom development

Use this skill for frontend and backend iterations against the development
Compose project. It does not drive the canonical `loom-demo` deployment.

## Failure-loop discipline

### Own and close the complete lifecycle

One assigned Luna lead owns a case through diagnosis, focused regression,
correction, same-case browser reruns, and final evidence. In-scope harness repairs
do not require a foreground handoff for every predicate or selector. Escalate
product decisions, shared architecture changes, or unexplained failures; Sol
reviews the complete verified unit before the lead commits or merges it.

Use actual retained request/response shapes and native event ordering as the
regression fixture. Exercise the driver's production helper, including negative
identity cases, rather than copying its matcher into a test. Distinguish browser
request IDs from diagnostic IDs and response arrival from evidence-body capture.
Do not impose a new wire contract merely to satisfy a test assumption.

Audit the remaining workflow actions and batch demonstrated driver corrections
before the next full rerun. Preserve independent data expectations, complete
lifecycle checks, and the original latency budget. Keep common evidence rules in
shared helpers rather than rebuilding them in each case.

Review the final artifact once; renew review for consequential corrections, not
unchanged passing checks or evidence-only bookkeeping. Record the pass in the
existing coverage matrix and commit the scoped unit promptly. Optional prose and
extra evidence packaging must not hold the next ready runtime case. Continue
independent implementation in isolated worktrees while runtime access is owned.

Measure elapsed diagnosis, implementation, focused checks, browser execution,
review, and integration separately where available; mark unmeasured time unknown.
Judge throughput by verified user lifecycles and closed product failures per
elapsed time. Harness repairs remain testing overhead. Reuse historical evidence
when its behavior and relevant source remain applicable; a different scenario
name or HEAD alone does not justify another browser run. Identify the concrete
changed behavior or missing proof before scheduling related cases.

### Worker-owned checks and bottlenecks

The assigned Luna worker runs and diagnoses its case, including reruns after
corrections. Return the exact command, exit status, elapsed time, report paths,
tested file hashes or source fingerprint, API identity, and remaining lifecycle
gaps with the patch. Sol reads the diff and evidence to accept or reject the
submission; do not routinely repeat a worker's passing checks.

When integration changes the tested artifact or creates a concrete shared risk,
assign the necessary combined check to a Luna worker. Sol runs a check only to
resolve a critical discrepancy that source and retained evidence cannot settle,
or when no worker can perform the required check. State that reason before
rerunning. Missing evidence goes back to the case owner rather than becoming
foreground testing work.

When progress slows, identify the limiting step: product diagnosis, harness
repair, test execution, review, integration, or shared runtime access. Use actual
run durations and the ready-patch queue, and label unmeasured time unknown.
Change assignments or integration order to relieve that step before increasing
worker count. Track closed lifecycles and elapsed time; worker activity alone
does not establish a speedup.

### Isolation and failure evidence

Before workers edit, resolve staging and deployed checkout with `realpath`.
Require physically distinct source files and preserve preimages for patch review.
A deployed checkout under `/tmp` is not an isolated stage. Follow `AGENTS.md`
for team-lead integration after Sol accepts the final artifact.

Before an isolated focused check, invoke the assigned-check guard from the owned
worktree with its absolute `--expected-root` and the live checkout's absolute
`--forbidden-root`, then pass only the check argv after `--`. For example:

```bash
node scripts/verify-ui/helpers/run-assigned-check.mjs \
  --expected-root /private/tmp/owned-stage \
  --forbidden-root /private/tmp/loom-construction-implementation \
  -- node --test scripts/verify-ui/helpers/tests/native-verification-bracket.test.mjs
```

The guard checks the physical working directory, Git top-level, and test path;
it permits `node --check`, `node --test`, `git diff --check`, and the registered
runner in checks-only mode. The runner route accepts only its exact in-repository
entrypoint, an explicit scenario and case, and `--checks-only`; it rejects a
symlinked or escaped entrypoint and every browser-mode option. The runner then
uses the selected registry case and its existing focused-check planner, including
registered Vitest groups. This keeps the Vitest argv policy in one place. It
guards where the command runs, not direct file edits. Edit only absolute paths in
the assigned worktree and set the command's working directory there.

For a registered fast gate such as Membership, put the runner invocation after
the guard's `--`:

```bash
node scripts/verify-ui/helpers/run-assigned-check.mjs \
  --expected-root /private/tmp/owned-stage \
  --forbidden-root /private/tmp/loom-construction-implementation \
  -- node scripts/run-native-verification-bracket.mjs \
    --scenario cda-current-draft-membership --case membership --checks-only
```

Retain per-file hashes, aggregate fingerprint, and API build identity before a
browser run. Compare afterward and identify changed paths. Source mutation
invalidates the run; its apparent UI failures are not reproduced product bugs.

After partial staging from a dirty worktree, inspect the staged diff and run
the focused check against an isolated snapshot of the index tree or resulting
commit. A dirty-worktree pass does not prove the staged artifact.

Capture diagnostics on the first failing action: elapsed time, control values,
DOM, console exception, exact owned request scope and draft/stage identity,
HTTP status, and response diagnostic body. Exclude credentials and unrelated
traffic. Unexpected network failures remain fatal.

For a failed native bracket, extract the retained request into focused test
input:

```bash
node scripts/verify-ui/helpers/extract-native-failure-input.mjs \
  --summary /path/to/summary.json \
  --browser-request-id browser-request-id-from-report \
  --request-id native-request-id-from-report \
  --out /private/tmp/native-failure-input.json
```

The extractor reads the domain and Playwright report paths from native
`summary.json`. It hashes artifacts at extraction when the summary has no
recorded hash; declared hashes are checked against the retained file.
Without `--check`, one failed assertion is selected automatically; multiple
failures require an exact name, while no failed assertion retains first-failure
context without an asserted check. The selected request and check do not
establish causal linkage. `--expected` takes a v1 `value-expectation` with a
matching source fingerprint and target, but its provenance declaration does not
prove independence. The output records `missing-independent-oracle` without
that file or `separately-supplied-provenance-unverified` with it; both set
`independenceVerified` to false. A captured response is observed output, not an
expected value.

Use the retained input in a focused regression, repeat the focused Node test
until it passes, then rerun the same bracket command in the Verification
bracket section with the same case, target, selection, and environment:

```bash
node --test /path/to/focused-regression.test.mjs
```

For each workflow, record preview correctness against independent source data,
native controls, Apply/Cancel where applicable, edit, removal/restoration, reload,
and latency. Mark each passed, failed, untested, or not applicable with a reason.
Partial assertions cannot close a lifecycle; Basic proof does not close CDA.

Account separately for product fixes, harness repair, environment repair, and
invalidated runs. Record run durations and available repair time; label unmeasured
time unknown. Use existing reports and the coverage matrix. Selector and fixture
repairs do not count as closed product failures.

After two consecutive harness failures on the same path, stop backend browser
reruns and audit the remaining actions in that workflow, not just the first
failing line. Check selectors and control multiplicity, fixture/schema shape,
request adapters, ownership, event ordering, saved state, and final error gates
against current source and retained DOM. Inspect cheap remaining assumptions
after the first failure when the evidence is already available.

Batch demonstrated driver corrections before the next full run. Validate
questionable locators with native Playwright on a small `page.setContent`
fixture; use retained request/response artifacts for adapter and classification
checks. Exercise the actual functions used by the driver. Include negative
cases that reject wrong identities and extra or unexpected errors. Check the
live fixture lifecycle: teardown-only records may not exist inside the workflow.
Preserve raw errors and classify expected events only with exact ownership and
response/supersession proof. Resume the same complete case with its original
oracle, lifecycle scope, and latency budget; do not weaken assertions.

### Verification bracket and compact handoff

Keep run orchestration in repository code. Ensure the owned stack is ready.
Use the combined command for a registered native case:

```bash
node scripts/run-native-verification-bracket.mjs \
  --scenario cda-current-draft-membership \
  --case membership \
  --target .codex/owned-cda-target.json
```

The runner loads the validated owned environment from the target config and
uses official Playwright `--list` to require exactly one registered test before
it runs focused checks. It binds the target config to the case identity, then
performs precheck, before/after capture, health, and one native Playwright test.
It creates a fresh summary outside watched source, forces one worker and zero
retries, and attempts after-capture and health even after browser failure. Do
not run a second manual bracket for the same case.

For a case without a registered target identity, load the validated owned
browser and capture environments, then use `--target-from-environment` with the
explicit native selection shown by `--help`. If the browser loader clears
`LOOM_CDA_*`, load capture environment after it. That mode is registry-unbound,
and the summary marks runtime dataset identity as not checked. Inspect the
case report and separate source evidence before accepting correctness.

Use `--checks-only` to run registered focused prerequisites without Docker or
Playwright. A case with no focused prerequisites is reported as `browser-only`;
a passing focused check is not a browser pass. The low-level
`owned-stack-verification.mjs` and `capture-owned-verification.mjs` commands
are for diagnosis. After a browser failure, compare source, docs, API identity,
and owned mounts, and report missing closure as unverified.

CDA fixture navigation defaults to `cdaUiRouting: 'explicit-query'`. Its
navigator requires the owned UI origin and root, one project matching the
fixture, one valid Explorer, and one `mode=builder` or `mode=viewer`; it passes
through only the exact raw URL `'about:blank'` for a page reset. A test that
intentionally verifies Vite-provided routing defaults must opt in with
`cdaUiRouting: 'defaults'`, which retains the existing project and bootstrap
Explorer checks.

Accept a pass only when the summary reports passed lifecycle and integrity,
all registered focused checks pass, and the selected native test exits
successfully. Read the linked domain report for correctness and consequential
failure evidence.

The first live proof passed the Group-to-Pivot Append lifecycle at source commit
`d120b68c7bf172ef2a0979052802f7108158363b`: 31/31 required checks, no retries,
23.579 seconds for Playwright and 44.284 seconds for the complete bracket.
This proves the coordinator on that basic fixture; it does not establish CDA
coverage or replace the remaining-workflow audit.

Generate the run summary from retained artifacts: exact command, exit status,
stage durations, source fingerprint, API identity, lifecycle checks and gaps,
maximum measured latency, failure category, and evidence paths. Add the patch
hash, preserved preimages, focused check results, and Luna max verdict once per
final reviewed artifact. Link to detailed logs instead of copying payloads or
manually reconstructing manifests. Sol reviews the consequential diff and this
packet; missing evidence goes back to its owner, not to duplicate foreground
runs. Renew review when the patch changes.

Give the runtime bracket one owner through after-capture and release. Keep
isolated implementation and review moving; a documentation commit or unrelated
packet must not hold a ready browser run. Record preparation, execution, review,
and harness repair separately so shorter browser duration is not reported as
faster delivery.

Keep routine verification near the agreed 20% time budget; broaden for concrete
shared risks or unresolved failures. Long-run updates report newly reliable
workflows, remaining product failures, and harness-repair time separately.

## Required Builder coverage

Inventory every visible dataframe-building feature. Read the bounded coverage
rules in [features/README.md](features/README.md). Give every feature one complete
browser lifecycle: open, configure, inspect enabled/disabled controls, obtain the
automatically rendered result, compare values and identities with independent
source records, Apply where present, reload, edit, remove, and verify restoration.
An API 200, query timing, or screenshot alone cannot establish a browser pass.

Test combinations by changes to row identity, multiplicity, available columns,
and retained source/relationship bindings. Cover each distinct transition once;
do not enumerate every sequence or repeat equivalent cases for every FHIR type.
Keep reported failures as executable browser regressions with their preceding
operations. Standalone operation tests cannot close composition regressions.
Run an exploratory pass of at most ten additional sequences per wave; choose
new contracts or data shapes, and record gaps instead of looping on permutations.
Required features and known regressions are not subject to that exploration cap.

Record DOM usability, result correctness, persistence, and performance separately,
with the invocation and evidence path. Distinguish passed, failed, untested,
skipped, and unreachable. Capture JavaScript exceptions, failed module loads,
unexpected 4xx/5xx, dead controls, clicks, and action-to-render time including
discovery and compilation. More than five seconds on CDA fails performance by
default. The current user-approved exception is limited to full-population root
quantity Pivot and related text-only quantity Pivot: category discovery and
native Pivot action-to-render checkpoints may take up to ten seconds. The
category scanner has a ten-second server deadline; construction proposal
preview already uses the existing ten-second preview runtime, and the browser
request transport allows thirty seconds. The bounded Pivot fixture and every
other operation retain the five-second acceptance budget. This exception
changes the threshold, not the evidence: real-CDA Pivot performance remains
unverified until a fresh registered native run completes within ten seconds
and passes its independent correctness, lifecycle, and source/API integrity
checks. Historical reports and synthetic profiles do not establish that pass.
Expected validation errors must offer an understandable repair path;
INTERNAL_ERROR always fails. Record incidental asset errors explicitly.

### Performance escalation

Classify functional correctness and performance independently. A timeout or
latency-budget miss is a performance failure; it does not prove an unsupported
operation or a functional bug. If execution never returns, correctness remains
unverified. A wrong result, crash, or broken control still needs a functional
regression even when it also causes excessive work.

Confirm timing failures serially on stable source and the same scoped data.
Separate browser wait, compilation, index preparation, query execution, and
render time before choosing a fix. Try a low-cost correction when evidence
identifies redundant work, a missing index, or another local cause. Compare the
same operation before and after with unchanged population, authorization,
limits, and correctness checks.

After two measured local corrections miss the same budget, stop hill climbing
and profile the execution structure before another rewrite. Escalate sooner
when scans, fan-out, large intermediates, or memory limits already show a
structural cost. Record actual scoped input and intermediate cardinalities,
phase timings, selected indexes, scanned rows, peak memory, and repeated work.
Distinguish backend execution from diagnostic cursor-draining overhead;
index-entry counts are not automatically population counts. Use the existing
owned query probe and retained query/bind artifacts. Mark unavailable measures
unknown rather than inferring them from EXPLAIN estimates.

Use that profile to compare execution strategies and their expected cost before
implementing the next candidate. Preserve null/missing values, empty parents,
multiplicity, membership, authorization, project, and generation semantics.
Do not hide controls, add filters, lengthen waits, or raise resource limits to
make the existing performance gate pass. Validate candidate correctness and
performance separately, then rerun the same complete browser lifecycle.

Build an integrated change before updating the shared stack. Freeze watched
source throughout browser verification; workers prepare incomplete edits in an
isolated checkout. A source/binary change invalidates the run. After a functional failure,
inspect the actual response and exception, fix the owning boundary, health-check,
and repeat from a fresh page. Route timing failures through performance escalation. Do not declare the UI fixed while unfinished edits
are still breaking its build.

Use the existing single Docker stack and loaded CDA data when requested; discover
its ports rather than assuming the synthetic defaults. Use bounded independent
source oracles and owned disposable QA tables. Verify cleanup only after the
workspace loads, then verify absence after reload; a blank page proves nothing.
Use bounded publication cases and independent ClickHouse checks.

## Existing synthetic driver

The commands below describe the older synthetic fixture driver, not complete
coverage of the current Builder. Check script existence and current controls
before running them. Adapt manual Preview paths to automatic rendering for V2.
Missing scripts and stale selectors are harness gaps, never passes. Extend the
real browser driver for required features and maintain honest coverage status.

## Official Playwright Test migration

Read [the official-runner migration record](../../../docs/PLAYWRIGHT_TEST_MIGRATION.md)
before changing a browser case. The target is native `@playwright/test`
discovery, fixtures, assertions, steps, deadlines, cleanup, and standard JSON
reporting. Keep Loom-owned data setup, independent correctness oracles, scoped
diagnostics, source/API identity, and lifecycle assertions in native fixtures
and tests; a test must not invoke an old browser script as a subprocess or
launch a second browser.

Use `scripts/verify-ui/registry.mjs` for current registered-case mappings and
`docs/verification/playwright/discovery.snapshot.json` for static discovery.
Neither establishes runtime coverage. The conversion ledger and durable runtime
reports in `docs/verification/playwright/` record their separate evidence;
`docs/PLAYWRIGHT_TEST_MIGRATION.md` describes selected checkpoints and gaps.
Run the static migration gate from the repository root after installing
both existing workspaces:

```bash
npm ci --prefix ui
npm ci --prefix scripts
node scripts/maintenance/playwright/check-native-playwright.mjs
```

The gate checks registered-case mappings, remaining custom browser ownership
(including the construction-preview benchmark driver), and undefined workflow
bindings using the UI workspace's existing TypeScript dependency. It is a
static migration check, not runtime, benchmark-performance, or browser evidence.
Select the scenario by `id` in `scripts/verify-ui/registry.mjs`, then select its
case with `scenario.cases[caseName].playwrightTest`. Grep for that native test's
exact title or case text. Keep unknown or unregistered mappings explicitly
pending; do not invent a runner command for them. For example, the registered
`builder-authoring/group-entry` case can be selected as follows after the
owned development environment is configured. This is an invocation example,
not a reported pass:

```bash
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs builder-authoring.spec.mjs --grep 'standard Group entry proposes an empty-key COUNT_ROWS preview'
```

Use Playwright locators and native actions for navigation, clicks, fills,
selection, and waits. Require a unique visible, enabled, pointer-receiving
target. A read-only page inspection can collect rows or transient state; it
must not invoke application handlers or set application control values. Keep
per-file hashes, an aggregate source fingerprint, and API build identity before
and after the run; source mutation invalidates the run. Retain the independent
fixture or CDA oracle and the complete lifecycle requirements above.

Use the JSON report and sanitized JSON diagnostics as primary failure evidence.
Retain the first failed action, locator state, DOM, console and owned request
diagnostics, and elapsed time. Traces and screenshots are optional follow-up
artifacts, disabled by default in the native runner. Run from a physically separate source
checkout with the source frozen. Use a dedicated Compose project for isolated
state by default. A single explicitly named, owned development stack with a
loaded CDA may be reused when authorized and its project and generation are
validated; this flow does not require a second Compose project. Default to
parallel ownership for independent functional cases. Each worker must own a
fresh QA project or a distinct disposable Explorer in the validated CDA project,
plus separate JSON and artifact paths. Cases that mutate the same saved Explorer,
publication target, source, or runtime must run in dependency order. Workers stage
fixes separately under the assigned team lead. Sol owns final acceptance;
the accepted lead owns scoped integration. Keep the shared source checkpoint
frozen throughout the batch. Follow the user's current team limits and ownership
rules: at most three teams, each with up to three Luna xhigh workers and one
Luna max lead. Assign distinct implementation, evidence, or review work; refill
completed assignments when useful work is ready. Avoid duplicate diagnosis and
idle assignments solely to meet a worker count.

Use the native runner's output controls for each independent process after
configuring the validated target from the run plan:

```bash
mkdir -p "$REPORT_DIR"
PLAYWRIGHT_JSON_OUTPUT_FILE="$REPORT_DIR/results.json" \
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs "$SPEC" --grep "$CASE" --output "$REPORT_DIR/artifacts"
```

Give every process its own `REPORT_DIR`. Select self-seeding cases with distinct
owned QA data; an existing seed is a dependency, not permission to share writes.
The official `--output` option isolates artifacts, and
`PLAYWRIGHT_JSON_OUTPUT_FILE` overrides the configured JSON report path.

Label latency measured during concurrent runs provisional. Confirm a timing
failure serially before calling it a product performance defect; do not suppress
functional, browser, or network failures. A passed historical report remains
useful but is not current proof: `node scripts/verify-ui/helpers/coverage-status.mjs`
shows status and source/build freshness separately. Missing API build identity
is unknown.

Run registered workflows through the official native test runner:

```bash
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs builder-authoring.spec.mjs --grep 'named cohort recoding preserves raw ALL bindings across edit and reload$'
```

Use `scenario.cases[caseName].playwrightTest` in the registry to select the
spec, then `--grep` for the case. The native basic fixture creates a fresh
disposable project with the case's declared source files. Real CDA follow-up
uses the native CDA specs and `LOOM_CDA_*` target options on the already loaded,
validated owned stack. It does
not upload the large dataset again. Retired standalone browser modules are
workflow libraries; do not invoke them with Node or the old reuse flags.
See `docs/PLAYWRIGHT_TEST_MIGRATION.md` for checkpoint status. Source conversion
and test discovery do not establish runtime or lifecycle passes.

## Launch

Start or attach to the isolated stack:

```bash
make dev
```

The target uses `loom-dev`, `http://127.0.0.1:8180`,
`http://127.0.0.1:3180`, and `testdata/devloop-fixture`. Source edits flow
through the mounted Go and Vite watchers. Use `make dev-rebuild` only after a
dependency, toolchain, or development image change.

## Doctor

Check the service and fixture without opening a browser:

```bash
make dev-doctor
```

Proceed only after `DEV_DOCTOR_PASSED`. The report records the validated
Compose project, fixture project, fixture generation, API status, and UI
status.

## Drive

Run the browser path:

```bash
make verify-fast
```

This command selects the native Playwright development journey on the synthetic
fixture; it does not establish CDA coverage. Focused Append coverage runs with
the Playwright Test command above. It uses accessible roles,
labels, and visible text to create a new per-run Explorer,
create a table, choose Patient as the root, add the supported
`Patient -> Observation` relationship, choose nested and scalar fields, click
Preview, and click Publish. It then reads the published materialization through
the API as independent proof, opens Viewer, loads and applies a filter, clicks
Download CSV, parses the CSV, and reloads Viewer.

Every run uses a unique `loom_dev_verify_<run-id>` project and checks that it
has no Explorers or fixture generation before seeding. The stable
`loom_dev_fixture` project and `loom-dev-bootstrap` Explorer are used only by
Launch and Doctor. Verification projects and materializations are retained:
the backend has no Explorer-delete operation. `make dev-down` stops only the
owned stack, while `node scripts/loom-dev.mjs dev-down --purge` removes its
exact development volumes.

Run the timing and recovery checks with:

```bash
make verify-full
```

The driver performs a harmless aliased package-source CSS edit and restore,
then creates an exact compiled Go success probe and proves its unique marker
executes in a fresh binary. It removes that probe, creates a separate
syntax-error probe, requires the stale API to stop and the current probe name
to appear in logs, then restores the probe and requires a fresh build stamp and
`/readyz` recovery. Source restoration has an identity guard. Playwright Test
owns browser teardown. Run `verify-current` and `verify-full` alone because
their HMR probes temporarily modify watched source.

## Evidence

Each browser run retains `.artifacts/loom-dev/<run-id>/report.json` alongside
its DOM evidence. `.artifacts/loom-dev/report.json` holds the latest command
report. These reports contain `status`,
`scenario`, `target`, `assertions`, `timings`, and `evidencePaths`.

Evidence includes initial, preview, and post-reload DOM snapshots, the parsed
CSV, the new materialization identity, and the failed-build log when the full
path runs. The driver does not write credentials, raw network traces, or
authorization headers.

## Cleanup

Stop the services and keep the isolated database volumes:

```bash
make dev-down
```

Remove only the validated development volumes when the fixture data is no
longer needed:

```bash
node scripts/loom-dev.mjs dev-down --purge
```

The cleanup command validates the complete owned Compose identity, service
ports, source mounts, and labeled volumes before it starts. It rejects
`loom-demo`, `NCPI_ACCEPTANCE`, and any other unowned project, and never calls
an unsupported Explorer-delete endpoint.

## Helpers and feature map

Use `scripts/owned-query-probe.mjs` to inspect a raw AQL file against the owned
development Arango database. It checks the Compose container, project and
generation binds, authorization binds, and the parser and EXPLAIN read-only
result. It defaults to EXPLAIN only. Add `--execute` to run the same query once
after those checks pass. The execution limit is at most eight seconds and 256
MiB. An expected-index mismatch stops execution. Reports include source paths
and hashes, plan and scan metadata, and timings. They omit query text, bind
values, result rows, and raw Arango messages. Choose a fresh output path.

```bash
node scripts/owned-query-probe.mjs \
  --query "$QUERY_FILE" \
  --bind-vars "$BIND_VARS_FILE" \
  --output "$REPORT_DIR/query-probe.json" \
  --expected-index "$EXPECTED_INDEX"
```

Append `--execute` to run the query and collect its bounded execution
statistics. The default runtime and memory limits are eight seconds and
268435456 bytes. The command refuses to replace an existing report.

The probe tests use only local fake subprocesses:

```bash
node --test scripts/owned-query-probe.test.mjs
```

Run session-safety tests with:

```bash
node --test scripts/loom-dev.test.mjs
```

Reuse the shared Go build cache across stages instead of creating per-stage
caches. Measure free disk before heavyweight checks and stop if it is
insufficient. After an accepted handoff, remove only task-owned build caches
and dependency copies; retain source patches and evidence.

The implemented feature map is in [features/README.md](features/README.md).
The implemented checks are:

| Feature | Driver proof |
| --- | --- |
| Isolated target | Compose project, ports, volumes, and fixture validation |
| Source iteration | Vite CSS HMR and Air build recovery in `verify-full` |
| Builder authoring | Explorer creation, table creation, root and relationship controls |
| Preview | Literal fixture rows, nested family values, related Observation value, and physical-column contract |
| Publication | New runtime and materialization identity for the current fixture generation |
| Viewer | Filtered rows, parsed CSV, and data after reload |

The fixture intentionally does not claim correctness for multiple related
resources projected with `FIRST`. The existing `verify-loom-ui` skill targets
the authenticated Kubernetes Builder and must not be used for this local
workflow.
