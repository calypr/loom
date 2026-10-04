# Playwright migration handoff

Review checkpoint: `infra/playwright-verification` in the physically separate
`/private/tmp/loom-playwright-verification` worktree, based on
`af1e706183dcd813c30276b3ee89e2f08cae463a`. Do not merge this branch into
`feature/construction-workspace` without reviewing the open gaps below. The
watched product checkout and its deployment were not edited or driven.

## Integrated progress

- All registered `scripts/verify-ui` cases use Playwright. The custom
  `scripts/verify-ui/browser.mjs` and unused `scripts/lib/browser.mjs` CDP
  drivers and their legacy tests were removed.
- Standalone CDA browser drivers, the generic verify-fast/full flows, and J01–J05
  use Playwright actions. Browser controls require unique, visible, actionable
  targets. Focused tests reject ambiguous, disabled, intercepted, and read-only
  controls, wrong oracle values, and lost persistence.
- Builder cases migrated through this checkpoint include Patient related
  Preview/Apply/reload/edit/removal, Preview limits, table management, bounded
  row-choice inspections, column presentation, Filter lifecycle, and the
  related-source chooser. Inspection-only cases retain `partial` status.
- CDA source oracles, project/generation scope, request evidence, five-second
  action gates, source fingerprints, build identity, first-failure evidence,
  and trace redaction remain in the migrated drivers.

## Executable evidence

- Complete related-row-to-Unpivot lifecycle passed on the isolated CDA stack:
  `/private/tmp/loom-playwright-related-unpivot-evidence-rerun4/report.json`.
  This is historical for its recorded source/build snapshot.
- The Add columns dialog case passed against the current branch at commit
  `b3d0fcad` with three dialogs and 21 actions, maximum 1,474 ms:
  `/private/tmp/loom-playwright-add-column-dialog-current-evidence/report.json`.
  Later commits make this historical, though its source/build freeze matched
  within that run.
- The current integrated Builder module suite passed 20/20 focused tests:
  `node --test scripts/verify-cda-builder-{patient-related,preview-limits,table-management,row-choice-inspection,column-presentation,filters,related-source-chooser}.test.mjs`.
  The registered `verify-ui` suite passed 58/58; the combined `loom-dev` and
  Builder module suite passed 96/96 at this checkpoint. `find scripts -name
  '*.mjs' -print0 | xargs -0 -n1 node --check` also passed. Rerun these before
  merge after any further integration.
- The five-second CDA Builder Publish render failed at 14,690 ms:
  `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu325aa-18d89e9.json`.
  Group-edit saved Preview and post-Unpivot ID-count render also failed the
  five-second gate in their retained reports. These are product performance
  findings for the product-fix owner, not migration passes.
- The standalone filter lifecycle runtime remains unverified. One run exposed
  an incorrect first-table entry assumption and retained evidence at
  `/private/tmp/loom-playwright-filter-lifecycle-evidence-rerun/filter-lifecycle.json`.
  After correcting it, the next run stalled without a report and was stopped.
  Per the verifier failure-loop rule, diagnose its blocking stage before retrying.

## Open migration work

`node scripts/check-playwright-migration.mjs` reports two files:
`scripts/verify-cda-builder.mjs` still has legacy CDP branches, and
`scripts/loom-dev.mjs` retains the CDP exports they call. The prepared
`6701eb96c787b8252c5e6180b39964c2360768e9` legacy-driver deletion
commit must wait until the Builder callers migrate. Do not count a zero static
gate as a runtime pass. Most newly migrated CDA cases have only static tests.

`node scripts/verify-ui/coverage-status.mjs
.artifacts/loom-dev/c52d4223d857/verify-ui` currently reports 0/13
registered cases with passing evidence for the current source and API build;
four historical passes remain. The quantity Pivot browser code preserves its
raw MISSING/NULL/value and duplicate SUM/MAX assertions, but its isolated
full-population run was interrupted without a report. Backend Pivot fixes
remain owned by the product instance.

The isolated CDA stack is Compose `loom-dev-c52d4223d857`, UI
`http://127.0.0.1:30102`, API `http://127.0.0.1:8282`, project
`loom_dev_cda_playwright_c52d4223d857`, generation `cda-fhir-v1`.
Do not use the product instance's shared stack or watched checkout.

## Review suggestion

Review and cherry-pick the tested migration checkpoint if the integration
branch can accept a temporarily mixed Builder harness. Continue the remaining
Builder conversion in this separate worktree, then remove `loom-dev.mjs` CDP
exports and run the migration gate, focused tests, and owned runtime cases.
The migration branch has not been merged into the integration branch.
