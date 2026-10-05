---
name: verify
description: Verify Loom Builder features in the real local browser, including bounded operation composition, CDA correctness, persistence, performance, and publication.
---

# Verify Loom development

Use this skill for frontend and backend iterations against the development
Compose project. It does not drive the canonical `loom-demo` deployment.

## Failure-loop discipline

Before workers edit, resolve staging and deployed checkout with `realpath`.
Require physically distinct source files and preserve preimages for patch review.
A deployed checkout under `/tmp` is not an isolated stage. Sol owns integration.

Retain per-file hashes, aggregate fingerprint, and API build identity before a
browser run. Compare afterward and identify changed paths. Source mutation
invalidates the run; its apparent UI failures are not reproduced product bugs.

Capture diagnostics on the first failing action: elapsed time, control values,
DOM, console exception, exact owned request scope and draft/stage identity,
HTTP status, and response diagnostic body. Exclude credentials and unrelated
traffic. Unexpected network failures remain fatal.

For each workflow, record preview correctness against independent source data,
native controls, Apply/Cancel where applicable, edit, removal/restoration, reload,
and latency. Mark each passed, failed, untested, or not applicable with a reason.
Partial assertions cannot close a lifecycle; Basic proof does not close CDA.

Account separately for product fixes, harness repair, environment repair, and
invalidated runs. Record run durations and available repair time; label unmeasured
time unknown. Use existing reports and the coverage matrix. Selector and fixture
repairs do not count as closed product failures.

After two consecutive harness failures on the same path, stop browser reruns.
Validate selectors, fixture shape, event ordering, request ownership, and saved
state against source and retained DOM, then run a focused check. Resume the same
case after correcting the demonstrated assumption; do not weaken assertions.

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
discovery and compilation. More than five seconds on CDA fails performance.
Expected validation errors must offer an understandable repair path;
INTERNAL_ERROR always fails. Record incidental asset errors explicitly.

Build an integrated change before updating the shared stack. Freeze watched
source throughout browser verification; workers prepare incomplete edits in an
isolated checkout. A source/binary change invalidates the run. After failure,
inspect the actual response and exception, fix the owning boundary, health-check,
and repeat from a fresh page. Do not declare the UI fixed while unfinished edits
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

The current `verify-ui` registry declares 23 cases. A separate static discovery
snapshot at 2026-10-05 02:02 UTC lists 152 cases across 19 Playwright spec files;
discovery is separate from registry coverage and does not mean those cases are
registered or verified. The development journeys and miscellaneous workflows
are included in that snapshot. After benchmark porting and launcher cleanup,
discovery lists 155 main-suite cases and one dedicated benchmark case. The
ownership gate passes; exact inventory reconciliation and runtime verification
remain open. Run the static migration gate from the repository root after installing
both existing workspaces:

```bash
npm ci --prefix ui
npm ci --prefix scripts
node scripts/check-native-playwright.mjs
```

The gate checks registered-case mappings, remaining custom browser ownership
(including the construction-preview benchmark driver), and undefined workflow
bindings using the UI workspace's existing TypeScript dependency. It is a
static migration check, not runtime, benchmark-performance, or browser evidence.
Select a case from `scripts/verify-ui/registry.mjs` using its
`playwrightTests[caseName]` entry, then grep for that native test's exact title
or case text. Keep unknown or unregistered mappings explicitly pending; do not
invent a runner command for them. For example, the registered
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
validated; this flow does not require a second Compose project. Keep one
browser session at a time. A passed historical report remains useful but is
not current proof: `node scripts/verify-ui/coverage-status.mjs` shows status
and source/build freshness separately. Missing API build identity is unknown.

Run registered workflows through the official native test runner:

```bash
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs builder-authoring.spec.mjs --grep cohort-recode
```

Use `playwrightTests` in the registry to select the spec, then `--grep` for the
case. The native basic fixture creates a fresh disposable project with the
case's declared source files. Real CDA follow-up uses the native CDA specs and
`LOOM_CDA_*` target options on the already loaded, validated owned stack. It does
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

Check the service and fixture without opening Chrome:

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

This command is the legacy synthetic-fixture workflow; native Append coverage
runs with the focused Playwright Test command above. It uses accessible roles,
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
`/readyz` recovery. Source restoration has an identity guard and Chrome exits
before its temporary profile is removed.

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

Run session-safety tests with:

```bash
node --test scripts/loom-dev.test.mjs
```

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
