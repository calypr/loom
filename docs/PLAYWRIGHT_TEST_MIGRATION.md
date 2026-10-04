# Official Playwright Test migration

The earlier migration replaced Chrome/CDP actions with the Playwright library.
It retained a custom runner. That is not a completed Playwright Test migration.

The target is native `@playwright/test` discovery, test fixtures, assertions,
steps, deadlines, cleanup, and standard list/JSON reporters. Tests must not
invoke the old browser scripts as subprocesses or launch an extra browser.
Loom retains owned data setup, independent data oracles, scoped diagnostics,
source/API fingerprints, and lifecycle requirements as test fixtures and helpers.

## Ownership and design decision

Two shapes were compared: extracting domain workflows into native specs, and
adapting the existing registry executor to run inside Playwright Test. The first
is selected because it removes browser ownership and case-status handling from
the old executor. Adapting that executor risks retaining two owners for deadlines
and exceptions, and encourages new cases to copy the old orchestration.

The caller is `test(..., async ({ page, workflow, loomContext }) =>
appendWorkflow({ page, report: workflow.report, action: workflow.action },
loomContext))`. `page` belongs exclusively to Playwright. Loom fixtures own
the disposable data context and correctness attachment. Assertions propagate
to Playwright; a failed case cannot be converted to a successful returned report.

The five-second navigation cap is intentional for this integration unit. It is
stricter than the old unbounded reload and the thirty-second explicit waits.
The action-to-render performance assertion remains independent of that cap.

## First integration unit

Append is the first full lifecycle: source setup, exact eight-row oracle with
duplicate identities and null padding, Preview, Apply, reload, edit, Cancel,
edit Apply, removal/restoration, and reload. Discovery is not a browser pass.
Until a current report proves every required assertion, this workflow remains
unverified. Other library-based scripts remain explicitly unmigrated.

Run the focused native case from the repository root after installing scripts
dependencies and setting the five owned development environment variables:

```bash
npm ci --prefix scripts
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs append.spec.mjs
```

The config uses headless Chrome, one worker against the shared development
stack, no retries, and five-second action and navigation limits. Screenshots,
video, and traces are off. Standard JSON results and sanitized Loom correctness
attachments provide failure evidence. Fixture preparation and browser execution
must be timed separately; waiting is not product progress.

## Speed without weaker evidence

Run the smallest relevant workflow. Do not rerun all cases without a concrete
shared risk. Reuse an already loaded dataset only through validated ownership;
mutating cases retain fresh disposable state. Avoid fixed sleeps and redundant
navigation. Do not use force clicks, mocked successful backend responses, or
longer timeouts to make failures disappear.

Changing the automation engine requires evidence that engine overhead materially
limits the same complete workflow, with setup, backend time, and correctness
held constant. A transport microbenchmark alone is insufficient.

## Current verified unit

On 2026-10-04 the native Append case passed against the owned development stack
and its frozen working tree, including existing uncommitted product changes.
This is one of 22 registered cases migrated and verified; standalone scripts
remain a separate conversion/consolidation inventory.

- 80 native actions; every required lifecycle assertion passed.
- Playwright command: 19.2 seconds; browser lifecycle: 15.1 seconds.
- Fixture preparation: 1.6 seconds; slowest recorded action: 873 milliseconds.
- Source fingerprint: `b54f0984b5c5da70f01ce3f49890ad904ed178982f02df2b6aec447e6f93d892`.
- Source and API build identities stayed unchanged; no unexpected errors.
- Favicon 404 retained as an incidental asset failure. Two canceled capability
  requests retain exact binding and successful superseding-request evidence.
- Focused oracle and network-classification tests: 27 passed.

Retained native evidence: `/private/tmp/loom-native-append-v4-evidence/results.json`.
Sanitized domain report: `/private/tmp/loom-native-append-v4-domain.json`.
Earlier diagnostic-audit failure: `/private/tmp/loom-native-append-v2-evidence`.
The run proves this working-tree checkpoint, not a clean product checkout or CDA
coverage. Do not infer other cases passed from this result.
