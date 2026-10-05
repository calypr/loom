# Official Playwright Test migration

## First phase of the current Builder reliability goal

The Playwright Test migration precedes further Builder exploration. Finish the
mechanical conversion of all browser verifiers, including the nine embedded
`loom-dev.mjs` journeys, into native `@playwright/test` cases and fixtures.
Preserve independent source oracles, lifecycle assertions, owned data setup,
and five-second user-action budgets. Migrate callers before deleting legacy
Chrome/CDP launchers and custom runners; retain API-only tools.

Complete and integrate the mechanical batch before beginning behavioral testing.
Syntax checks and test discovery establish migration coverage only. Freeze the
integrated source checkpoint, then run the native cases, fix failures, and rerun
the same cases. Report harness defects separately from product defects, attach
bounded sanitized failure summaries and source fingerprints, and record skipped
or unrun cases as unverified. Migration is complete when no active browser path
owns a legacy launcher and the inventoried native cases have passing evidence
or an explicit unresolved blocker.

Then resume the existing CDA Builder reliability goal with its full feature and
transition inventory: preview, Apply/Cancel, edit/removal, reload, independent
correctness, and latency. Keep bounded exploration waves and roughly a 20%
routine verification budget. The migration does not replace or reduce that goal.

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
unverified. Other runner-based scripts remain explicitly unmigrated.

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
Append and Join have passed native runs at their respective checkpoints: two
of 22 registered cases. Standalone scripts remain a separate conversion and
consolidation inventory.

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

The native INNER/LEFT Join lifecycle passed next: 60 actions, every required
check, no unexpected errors, and unchanged source/API identities. The command
took 17.6 seconds; its browser phase took 13.2 seconds, with a slowest action of
870 milliseconds. Source fingerprint:
`5355d0136956cf8740e24a8fcddc39b690e1451897b55b07dc613d51f95d20f4`.
Evidence: `/private/tmp/loom-native-join-v1-evidence/results.json` and
`/private/tmp/loom-native-join-v1-domain.json`. Both Combine workflows now use
native specs; their old runner and executable module tail have been removed.

## Batch conversion checkpoint

The operator changed sequencing on 2026-10-04: finish all mechanical conversions
before running the combined test batch. Preserve the original oracles and
lifecycle assertions, integrate ready patches in parallel, then freeze source
and run native cases. Investigate failures against that same checkpoint.
Syntax and discovery checks may catch broken extraction before the freeze;
they are not browser evidence. The earlier one-workflow-at-a-time sequence is
superseded for this migration.

Nullable Join passed its full native lifecycle before this sequencing change:
26 timed actions, all required checks, no unexpected errors, 16.7 seconds for
the command and 12.5 seconds for the browser lifecycle. Its source fingerprint
was `3f9d1f894c4b6cb3bfc882eaf2a2313206d25517a051a1b4b1026cd527ba69da`.
Evidence is `/private/tmp/loom-native-nullable-v5-evidence/results.json` and
`/private/tmp/loom-native-nullable-v5-domain.json`.

Four earlier runs repaired harness assumptions: the preview is outside the
proposal panel, removal restores the original empty target, exact version-1
empty construction is semantically equivalent to absent construction, and
Explorer creation checks must actually be recorded. These were harness repair,
not product fixes. Raw rows, scoped receipts, Cancel state, and source documents
remain checked. The established strict empty-construction normalization is
shared with the existing Combine oracle; other document changes remain errors.

Controls (three cases) and load recovery (two cases) are mechanically integrated
but have not run natively. Shared fixture changes have also landed in the
working tree. This makes eight converted registered cases and three historical
browser passes; it does not establish an eight-case suite pass.

Draft Combine (three cases) and Viewer query (one case) are now mechanically
integrated as well. Twelve registered cases have native source ports; the
combined browser pass remains pending until the conversion batch is complete.

The first standalone source ports are integrated for Add columns dialogs,
related-record eligibility, and publication with Viewer reload. These are
mechanical source ports, not new browser passes. The integrated registered
workflows now cap inherited browser waits at five seconds; API and fixture
preparation timeouts remain separate. Viewer action readiness uses native
retrying assertions instead of immediate disabled-state checks.

The standalone inventory scans 85 root verifier sources plus one UI-package
source. It records browser consumers, retained API-only tools, pure oracles,
and obsolete historical prototypes separately. Source-to-native-case mappings
will be reconciled before the combined checkpoint is frozen.

All 22 registered cases now have native specs and direct workflow functions.
The old custom runner, DOM action helper, CLI dispatcher, and their test-only
compatibility exports are retired. The basic fixture context now supports the
owned fresh-fixture path directly. Historical helper tests were removed; the
Builder readiness and pure preview/oracle assertions remain. The combined test
batch is still pending completion of standalone ports and the shared CDA fixture.

## Standalone integration checkpoint

Official Playwright discovery now lists 91 tests in 13 spec files. This includes
32 Builder actions, ten CDA dialog/filter/root/publication cases, and 27 reshape
cases alongside the 22 registered cases. Discovery imports the integrated
modules successfully; it is not browser or lifecycle evidence.

The CDA fixture accepts native dialog handlers and rejects failed domain
assertions as well as unexpected network and console diagnostics. Independent
reshape cases use normal sequential execution so a failed case does not skip
the remaining cases. Standalone scenarios without a registered lifecycle
contract retain an unverified domain status even when individual assertions pass.

Field and row consumers, the remaining collection scripts, and legacy launcher
cleanup are still being converted. Finish these ports, reconcile source-to-case
coverage, and freeze the integrated checkpoint before the combined test batch.
