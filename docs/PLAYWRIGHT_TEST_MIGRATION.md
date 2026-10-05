# Official Playwright Test migration

## October 5 migration checkpoint

The current registry contains 24 cases. Main Playwright static discovery lists
160 tests in 23 spec files; the discovery snapshot declares 164 cases across
five scoped sessions (the main suite, one separately configured benchmark, and
three scoped one-case lists). The static native ownership and binding gate
passes all 24 registered cases.
These counts and gates establish discovery and static coverage only.

Selected durable native runtime results:

- Epoch 6 `CDA compound fields` passed its native lifecycle with 47 recorded
actions and unchanged source/API identities:
`docs/verification/playwright/runtime/compound-fields-epoch6.json`.
- Epoch 6 `CDA contributor exists` passed the official runner 1/1 with no
skips, unexpected results, or retries. Its generic report does not carry the
workflow's required-check aggregate, so this is a runner pass rather than a
registry-complete coverage claim:
`docs/verification/playwright/runtime/contributor-exists-epoch6.json`.
- Epoch 6 `CDA source fields` passed the native Playwright/domain lifecycle;
its required-check list is empty, and persistence/performance aggregates remain
untested: `docs/verification/playwright/runtime/source-fields-epoch6.json`.
- Epoch 7 `CDA coded fields` passed the official test and 52 workflow
assertions/actions with unchanged source/API identities. Its generic report is
unverified and concurrent timing is provisional:
`docs/verification/playwright/runtime/coded-fields-epoch7.json`.
- Epoch 8 `builder-authoring/repeated-empty` passed the standalone Basic
fixture lifecycle, including literal `component: []`, a missing component,
populated items, PRESERVE_PARENT and EXCLUDE, Cancel/Apply/reload, and
restoration to RECORDS. Official exit 0; 125/125 assertions and 53/53 required
checks passed. Source/API stayed unchanged; timing is provisional under
concurrent runs. This does not prove literal-empty CDA EXPANDED-to-GROUP
composition, which remains partial in coverage row 123, or GROUP authoring:
`docs/verification/playwright/runtime/repeated-empty-basic-epoch8.json`.
- Epoch 9 `CDA source fields` passed the official native case 1/1 with 25
recorded assertions and 23 actions. The full direct-field lifecycle covers
scalar and repeated source values, proposal Cancel, Apply, rename, reload,
removal, and final reload against exact selected raw CDA identities and values.
Source, documentation, and API identities stayed unchanged. Max action latency
was 592 ms during overlapping cases and is provisional, not a serial benchmark.
The fixture report remains `unverified` because its required-check list is
empty; the native Playwright case and all recorded assertions passed. Related
semantic sources and authored EXPAND remain separate gaps:
`docs/verification/playwright/runtime/source-fields-epoch9.json`.

Epoch 10 `CDA contributor rules` subsequently passed the full native case: 42
assertions and 40 actions, exact EQUALS filtering against raw 0/1/7-related
Observation witnesses, Cancel/Apply, reload, EXCLUDE edit, removal, and source
restoration. Source/docs/API identities were unchanged. Maximum action time was
300 ms, provisional under concurrent execution. The generic report retains its
empty-required-check metadata limitation. Evidence:
`docs/verification/playwright/runtime/contributor-rules-epoch10.json`.
Epoch 11 `CDA repeated contributor ANY` also passed: 53 assertions, 51 actions,
and 26 workflow checkpoints including nested-code EXISTS/EQUALS, Cancel/Apply,
policy edit, reload, removal, and source restoration. Its serial maximum
recorded action time was 336 ms; the longest workflow checkpoint was 2,204 ms.
Source/docs/API were unchanged and there were no unexpected errors. Duplicate
code occurrences within one Observation remain unexercised by the bounded CDA
witnesses. Evidence:
`docs/verification/playwright/runtime/contributor-any-epoch11.json`.

Epoch 14 `CDA collection repair` passed the same native partial-long-route
case 1/1. The lifecycle removes the unmapped member from the three-member
selection, preserves the exact Observation → Specimen → parent route, reloads
the two mapped Observation IDs, clears to authorized rows, reselects and
reattaches the repaired collection, then reloads and verifies the same two IDs.
The clear preview returned 25 authorized rows (`aria-rowcount=26`, including the
header); the virtualized table exposed 20 visible ID cells, all matched against
the scoped raw Observation membership oracle. The final collection coverage was
2 selected, 2 producing rows, 0 needing attention. The maximum critical workflow
step was 2,982 ms, provisional because this case overlapped the independent
related ONE/ALL case. Source, docs, and API identities remained unchanged. The
generic report has an empty `requiredChecks` list and leaves aggregate
correctness, persistence, and performance untested; the native case's specific
raw-membership, route, reattachment, and reload assertions passed. Evidence:
`docs/verification/playwright/runtime/collection-repair-epoch14.json`.

The epoch 9 `CDA contributor ANY` and `CDA contributor rules` runs passed the
previous stable-table locator and advanced into diagnostic accounting, but
their official tests ended failed. ANY reported `Only an exact observed stale
proposal abort may be classified` (`1 !== 0`); rules reported `Mode change may
classify at most one superseded request on each exact owned path`. These remain
unverified harness/diagnostic outcomes; they do not establish product workflow
failures or passing lifecycles. Their raw results are under
`/private/tmp/loom-native-parallel-wave9/{any,rules}/results/`.

Historically, those three epoch 9 cases first failed in epoch 8 before workflow
actions because a shared locator included a decorative `▤` icon in the role
name. Epoch 9 source fields passed the full native lifecycle, while ANY and
rules reached later diagnostics. Epoch 9 closed with source, documentation,
and API identities unchanged; source fingerprint
`a8711c9d98001708135b725f4c704b0f28dd73bf7049e4ecc9a3d7806e7160f6` (1487
files), no changed paths, and unchanged API build identity. Closure record:
`/private/tmp/loom-native-parallel-wave9/closure.json`.

This is a selected checkpoint list. Other retained runtime reports remain
separate evidence; cases without scoped passing runtime evidence remain
unverified. Discovery and static gates do not change that status.

## Immediate integration priority: public verifier commands

Audit `make verify-fast`, `make verify-full`, all other browser verification
targets, and references to the retired `verify-ui` launcher before expanding
the browser case inventory. Each supported command must invoke native
Playwright Test explicitly. Remove obsolete targets and document their native
replacement rather than leaving a retired script callable from Make.

The public Make targets now invoke Playwright Test directly. All eleven targets
select exactly one native case with anchored titles, including distinct `j04`
and `j04-patient` selections. The root-settings target preserves three cycles.
The retired `verify-ui` launch example has been removed; registry and coverage
APIs remain. The worker's two existing dispatcher contract checks passed.
Discovery verifies command selection, not browser lifecycle success. The
source-mutating `verify-current` and `verify-full` cases must run alone.

## Current mechanical checkpoint

On October 4, native discovery lists 155 tests in 22 spec files, plus one
benchmark case in its dedicated configuration. All 23
registered cases have native spec mappings, and the static binding check finds
no undefined names. These results prove discovery and static coverage only;
most converted cases remain unrun. The first runtime results are recorded below.

The ownership and static binding gate passes. The benchmark uses the native
Test browser fixture; the old launcher and CDA browser session helper have
been removed. The standalone inventory is reconciled against exact discovered
titles. Its gate rejects both current-source hash drift and a deleted case mapping.

The targeted unit group passes 92 checks with zero skips after correcting one
stale screenshot-location assertion. It covers development journey contracts,
owned CDA target validation, report sanitization, API build freezing, and
native network evidence. The initial failure was a harness assertion, not a
product defect. Browser lifecycle evidence is still pending.

The existing loaded CDA stack is `loom-dev-6d7df93d6a37`, with API port 8188
and UI port 30008. Docker inspection confirmed its Compose working directory,
configuration file, and API/UI source mounts point to the implementation
checkout. The target guard now validates that ownership instead of rejecting
those names and ports unconditionally. Foreign checkout identities, mismatched
service ports, and foreign source mounts remain rejected. The updated guard
tests pass in the targeted unit group.

## Initial native runtime evidence

The corrected basic run on October 4 completed with one passing case and one
failure in 24.5 seconds. Its source fingerprint was
`66d09284d7ea894c4ab3885d310a769368e72b8b85f3801b7bd2200c4263819b`
before and after. The owned API identity also stayed unchanged.
Reports are retained at `/private/tmp/loom-native-basic-corrected-evidence`.

`builder-authoring/group-entry` passed its automatic empty-key `COUNT_ROWS`
preview and raw two-Patient oracle. The Group action completed in 816 ms.
This case does not cover Apply, Cancel, edit, removal, or reload; it is partial
workflow evidence rather than a complete Group lifecycle.

The compound basic case reached the coded chooser but found two `Height`
checkboxes, one decimal and one date/time. Its driver must select the decimal
source described by the independent quantity oracle. The same run also
captured an unexpected backend 409, `RECEIPT_RECOMPILE_REQUIRED`, while opening
Group on a selected Observation population. Scoped server logs identify
`RECIPE_CONTRACT_VIOLATION` during preview-plan compilation. The compiler's
keyless count shortcut incorrectly skipped source-row materialization for a
Group carrying population contributors. The contributor-aware guard now passes
the compiler regression and the real database preview returns count 1. The
driver selects the decimal Height candidate by its visible type metadata.

The next frozen run completed in 32.6 seconds with one passing case and one
failure. Its source fingerprint was
`c8908931e1fcb0674d1acc924802854c2b1bcdbbcc294f7e846440e166d603f0`;
the source and API identity stayed unchanged. Evidence is retained at
`/private/tmp/loom-native-compound-sanitizer-fix-evidence`. The compound case
passed its data, Apply, edit, Cancel, removal, and reload assertions, but three
aborted selection reads kept the overall case failed. The outer Builder and
workspace both load the handed-off selection during reload; the outer result
retires the workspace's duplicate request. Fix that ownership overlap before
accepting this lifecycle. Do not exclude these failures from diagnostics.

Harness corrections preserve exact public catalog SHA-256 snapshot identifiers
and boolean identity metadata in sanitized reports while still redacting
credentials. The focused sanitizer and request-capture group passes 9/9 tests.

After the ownership correction, the same two native cases passed in 31.6 seconds
with zero unexpected browser or network errors. The workspace now waits while
the outer Builder resolves a handed-off selection; standalone selection loading
still passes its regression. The basic compound case verifies exact decimal
Height values and Observation identity through preview, Apply, edit, Cancel,
removal, and reload. All measured result transitions complete within 1.5 seconds.
Source fingerprint
`c62fd107f21a4ee93a9081ea06d1ffffba6c2bdbf88919314a8472c1488ce8d0`
and API build identity stayed unchanged. The compact report is
`docs/verification/playwright/runtime/compound-coded-group-basic.json`; full
artifacts are retained at `/private/tmp/loom-native-compound-handoff-fix-evidence`.
This closes the basic compound lifecycle. Real CDA follow-up and other native
lifecycles remain unverified.

Earlier setup failures came from using the CDA project as the basic fixture's
bootstrap project. Correcting that environment exposed two stale assumptions:
a blank Explorer loads through GET `/builder`, and row-choice buttons have
accessible names distinct from their visible record-type text. Those harness
corrections preserve the original assertions. They are not product fixes.

## First phase of the current Builder reliability goal

The Playwright Test migration precedes further Builder exploration. Finish the
mechanical conversion of all browser verifiers, including the nine embedded
`loom-dev.mjs` journeys and `construction_preview_bench.mjs`, into native
`@playwright/test` cases and fixtures.
Preserve independent source oracles, lifecycle assertions, owned data setup,
and five-second user-action budgets. Migrate callers before deleting legacy
Chrome/CDP launchers and custom runners; retain API-only tools.

Complete and integrate the mechanical batch before beginning behavioral testing.
During testing, assign independent native cases to Luna xhigh owners by default.
Sol reviews evidence and staged fixes and integrates one coherent unit at a time.
Independent case execution continues while that integration is in progress;
use the local verification skill's isolation and performance rules.
Syntax checks and test discovery establish migration coverage only. Freeze the
integrated source checkpoint, then run the native cases, fix failures, and rerun
the same cases. Report harness defects separately from product defects, attach
bounded sanitized failure summaries and source fingerprints, and record skipped
or unrun cases as unverified. Migration is complete when no active browser path
owns a legacy launcher and the inventoried native cases have passing evidence
or an explicit unresolved blocker.

Before calling the migration verified, make
`node scripts/check-native-playwright.mjs` pass, reconcile the complete case
inventory, freeze source, and run the native browser cases. Diagnose failures
and rerun the same cases. Report passing lifecycles, unverified cases, product
failures, and harness failures separately.

## Second priority: Builder request ownership

Only after that verified migration checkpoint, read
`/private/tmp/loom-agent-architecture/docs/architecture/AGENT_FRIENDLY_PLAN.md`
and take its Builder request-ownership refactor as the next unit in
`/private/tmp/loom-construction-implementation`. Preserve all current uncommitted
work and do not create another worktree for this integration. The user selected
**full autopilot** for this later refactor. That authorization is recorded;
proceed after the verified migration checkpoint without asking again.

Then continue the existing CDA Builder reliability goal with its full feature
and transition inventory: preview, Apply/Cancel, edit/removal, reload,
independent correctness, and latency. Keep bounded exploration waves and
roughly a 20% routine verification budget. These priorities do not replace or
reduce that goal.

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

## Historical first integration unit

Append is the first full lifecycle: source setup, exact eight-row oracle with
duplicate identities and null padding, Preview, Apply, reload, edit, Cancel,
edit Apply, removal/restoration, and reload. Discovery is not a browser pass.
Until a current report proves every required assertion, this workflow remains
unverified. The unmigrated status below describes that earlier checkpoint;
current conversion coverage comes from the static gate and case inventory.

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

## CDA fields mechanical checkpoint

The eight standalone coded/compound/cohort/contributor/source-field drivers now
export native workflows under `scripts/verify-ui/`;
`standalone-cda-fields.spec.mjs` owns their Playwright cases. Their raw source
oracles and option variants remain. Arango queries use the validated fixture
container and reject a mismatched override. Source-to-case mappings are in
`scripts/playwright/standalone-cda-fields.mapping.md`.

Syntax checks and native discovery passed: 99 tests in 14 spec files. Browser,
unit, and runtime checks for this batch have not run. The fields lifecycle cases
remain unverified until the frozen-checkpoint testing phase.

## CDA rows and standalone mechanical checkpoint

The remaining CDA standalone helpers and the authored/cohort/repeated-row
workflows now run through native specs and fixtures. Source oracles, option
variants, and lifecycle assertions remain. Missing required source witnesses
produce explicit skipped/unverified reports; product failures stay fatal.
Failure diagnostics use bounded sanitized JSON with DOM/control state and
native five-second action deadlines. Exact expected 400/422 validation and
request cancellation are correlated to their captured request identities.

Syntax and discovery passed: 131 tests in 17 spec files. These are mechanical
checks, not passing browser lifecycles. The migration gate still names six
paths: the two launcher libraries, `loom-dev.mjs`, compound coded grouping,
upstream edits, and the package-local contributor-exists helper. Their ports
and launcher cleanup precede the frozen-source runtime phase.
