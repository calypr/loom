# Builder test cleanup handoff

## Goal

Make Builder verification help developers find and fix product defects. Reduce
the large JavaScript verification footprint by consolidating redundant tests
and retiring genuinely unused tooling. Preserve unique user-visible behavior,
independent data oracles, source-integrity checks, and negative controls.
TypeScript conversion is a later step; changing file extensions alone is not
the goal.

The user wants a short implementation loop. Tests should exercise real
frontend and backend code, produce actionable failures, and lead to product
fixes where the product is wrong. Avoid growing inventories, report machinery,
and formal adapters without demonstrating their value against actual code.

## Checkout and constraints

- Branch: `feature/deep-clean`. HEAD when this handoff was written:
  `ad531a0b6`.
- The working tree contains extensive uncommitted work from an earlier cleanup
  and this conversation. Preserve it. Do not reset, stash, clean, or overwrite
  changes to make verification easier.
- Read `AGENTS.md` and `docs/REPO_CLEANUP_GOAL.md`. The cleanup goal is an
  untracked, authored file in this checkout. The latter records the current
  cleanup queue and the test direction discussed here.
- Do not commit, publish, deploy, or rewrite history unless requested. Local
  browser checks use the project's `verify` skill. Serialize runs that mutate
  the same stack and freeze tested source for each browser run.

## What is already in place

The old MJS semantic catalogue was retired, but its history can be inspected
at `ad531a0b6:docs/verification/mjs-catalogue-decisions.tsv` and the source
catalogue at `ad531a0b6^:verification/builder-interactions/mjs-source-semantics.json`.
The earlier catalogue does not prove that a current file can be deleted. Check
registry references, dynamic discovery, CI, callers, and unique assertions.

The existing operation-history runner uses a bounded JavaScript model to
generate frontend actions, send captured requests through Go service code, and
exercise UI presentation of Go responses. In this conversation,
`scripts/verify-builder-operation-history.mjs` gained `--current-source`,
`--case`, and `--list-cases`. A selected case also runs two required witness
cases. Current-source mode records hashes and rejects input changes during a
run; it does not replace pinned-source acceptance. A selected normal run passed.
A temporary Go overlay that disabled dependent-step removal failed at the
intended READY-versus-NEEDS_REPAIR assertion. This is evidence for that one
backend defect, not proof of the whole app.

`scripts/verify-builder-group-slice.mjs` runs a bounded TLA+ Group safety model,
selected frontend lifecycle tests, a Go lifecycle test, and the native Group
browser case. The browser case's captured trace is replayed against TLA+.
The complete command passed on the local Basic fixture. The model does not
generate the frontend or Go tests, and the separate checks are not an
end-to-end proof of every possible frontend-to-backend interaction.

The command used for that run was:

```sh
LOOM_DEV_API_PORT=8181 LOOM_DEV_UI_PORT=3181 \
TLA2TOOLS_JAR=/tmp/loom-group-slice-tla2tools.jar \
node scripts/verify-builder-group-slice.mjs
```

The JAR is a temporary artifact. The runner verifies the pinned TLC checksum.
The local Compose project was `loom-dev-c613b0a6c645`, with API on `8181` and
UI on `3181`. Rediscover and validate the stack before reuse. The complete
Group run summary was
`/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-group-slice-misvKU/summary.json`.

## Completed in this conversation

- The native Playwright source gate now passes with 138 registered case
  mappings and 112 valid focused-check plans. Test repairs addressed a missing
  digest import, an invalid selector variable, browser callbacks capturing
  Node values, and a false removed-API identifier match in the trace validator.
- The Basic standalone coded-fields journey passed through Cancel, Apply, and
  reload with exact per-patient values. The selected-feature UI now displays
  source, system, and version qualifiers that were previously only accessible
  to assistive technology. Its component test passes. The browser summary is
  `/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-basic-standalone-coded-fields-coded-fields-mingk8/summary.json`.
- The Basic Pivot-to-Unpivot journey passed after updating its test driver for
  current row-intent and dialog behavior. It checked exact fixture rows,
  missing versus null categories, Cancel, Apply, edit, removal, reload, and
  restoration. The five focused oracle tests and native source gate passed.
  The browser summary is
  `/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-basic-pivot-unpivot-lifecycle-EdS5jL/summary.json`.
- These browser results are scoped to their synthetic Basic fixtures. They do
  not establish CDA behavior or independently checked runtime dataset identity.

The personal pstack plugin was updated to `0.15.36-codex.1`. The active
`~/.codex/pstack-models.json` was unchanged. The new `statechart` launcher was
checked for startup, but a complete statechart execution was not verified.
The old marketplace source was backed up at
`/Users/peterkor/plugins/pstack-backup-20261028-160447`.

## Unfinished and important

The uncommitted Group-twice extension in
`scripts/verify-ui/workflows/builder-group-entry.mjs` runs after the existing
single-Group trace is written and replayed. The extension's actions are not in
that formal trace, and it leaves the table grouped after the recorded outcome
describes restored source rows. The validator currently accepts at most one
persisted Group step. Do not present this extension as formally verified or
delete the standalone Group-twice journey on its account. Decide whether to
finish the integration with honest trace coverage or remove the unfinished
extension while preserving the standalone test and its assertions.

The semantic Specimen and expanded Coding Group workflow repairs have focused
oracle and static checks, but their CDA browser lifecycles have not run. The
formal maintenance skill was applied only as a focused preliminary audit of
Group; the broader Builder model inventory has not been audited or declared in
sync. The project-local formal skill has no explicit sync commit for the new
slice. Do not advance a sync marker without reviewing the covered code and
demonstrating that the check detects a real break.

## Continue

1. Inspect the full working-tree diff and the cleanup queue before editing.
   Identify one test family with a concrete product behavior and a runnable
   Basic fixture. Do not assume unreferenced `.mjs` files are dead.
2. Run its focused tests and the existing browser case. Fix test-driver drift
   where evidence shows the UI changed. Fix product code when a real user
   behavior is wrong. Keep independent expected values and original coverage.
3. Consolidate or delete an old verifier only after the replacement has
   demonstrated equivalent coverage on the relevant surface. Keep CDA and
   Basic claims separate. Record outcomes in the existing cleanup goal rather
   than creating another inventory.
4. For a narrow, high-value lifecycle, connect model-derived cases to actual
   frontend and backend behavior. Prove the checks fail on a controlled defect
   in the guarded code. Keep browser acceptance for UI wiring and real data
   behavior, and report model bounds and adapter limitations.
5. Once the useful test set is established, choose a TypeScript execution and
   type-check strategy for `scripts`, migrate related files and callers in
   bounded groups, and update the Node, Makefile, and CI entrypoints. Do not
   relabel handwritten JavaScript as generated to alter GitHub statistics.

The cleanup goal contains additional queued work. Use its ownership and
coverage rules before taking on those items. Preserve the user's preference
for product progress over accumulating test infrastructure.
