# Repository cleanup goal

## Status

Active. This goal continues the cleanup pass on `feature/deep-clean`.

The working tree already contains uncommitted cleanup: the retired Playwright
conversion-ledger chain, deleted dead helpers and stale scripts, test-only UI
adapters moved to `testSupport`, merged duplicate tests, and activated orphan
checks. Treat that work as owned. Do not reset, stash, clean, or overwrite it.

## Objective

Reduce the repository's maintenance surface without changing product behavior
or deleting the only proof of a live contract.

Success means fewer redundant files, fewer duplicate locations, and fewer
unowned checks with equal or better verification. Line count alone is not a
success criterion.

## Definitions

- **Cruft:** a file, export, check, document, or directory with no current
  consumer and no current behavior guarantee.
- **Zombie:** an old file that has no active reference, is not run by CI,
  Makefile, package scripts, registry discovery, or a validator, and is not the
  sole proof of a live contract.
- **Test-only code:** production-facing code imported only by tests. Move it to
  `testSupport` or a test fixture area; do not delete it while tests use it.
- **Duplicate location:** two or more files that own the same contract,
  inventory, command, migration record, or evidence. Keep one canonical owner.
- **Quarantined history:** an explicitly historical archive with an active
  validator or index. Retire it only as a unit after moving the validator and
  pointer.
- **Orphan check:** a test, audit, or checker that passes or fails but is not
  invoked by any runner.

## Decision rules

1. Age is a screening signal, not a verdict. A file older than four, eight, or
   twelve weeks is a candidate only after its references and guarantees are
   checked.
2. A test that still protects existing behavior stays unless equivalent
   coverage is proven. If it is redundant, cite the replacement before deleting.
3. Code imported only by tests is moved, not deleted. Production `src/` must not
   depend on test libraries.
4. A document with no active reader is not automatically dead. If its content
   is canonical, link it, move it to its owning area, or merge it; delete only
   after the useful decisions have a current home.
5. Generated reports and evidence are deleted only after checking active
   readers, validators, coverage rows, and retained-proof links. Preserve a Git
   recovery pointer when history matters.
6. Each contract has one canonical location. Do not keep two hand-maintained
   copies of the same inventory, migration record, or evidence map.
7. Preserve unrelated dirty work. Do not reset, stash, clean, rewrite history,
   or publish.
8. Do not replace a working check with a smaller-sounding check unless the
   replacement proves the same behavior.

## Inventory axes

Use these axes together. A candidate should survive more than one signal.

- **Reference graph:** exact path and basename searches, imports, registry
  `sourceFiles`, CI paths, Makefile targets, package scripts, and dynamic
  discovery.
- **Test-only code:** non-test modules imported only by `*.test.*`, `*_test.go`,
  or formal-test helpers.
- **Duplicates:** identical or near-identical utilities, docs, inventories, and
  migration maps in multiple locations.
- **Age:** last Git touch, excluding uncommitted files, used only to rank
  candidates.
- **Junk drawers:** directories mixing runtime state, fixtures, one-off tools,
  current tests, and historical evidence.
- **Superseded migrations:** completed one-off migrations and recovery tools
  that production has replaced.
- **Documentation graph:** incoming Markdown links, exact path mentions, index
  entries, and validators.
- **Runner coverage:** tests and audits that are not invoked by `make test`,
  CI, package scripts, or registry commands.

## Execution loop

1. **Inventory.** Build a candidate list with path, last-touch age, size,
   references, and current owner.
2. **Classify.** Mark each candidate delete, move, merge, wire, quarantine, or
   keep, with a one-line reason.
3. **Verify references.** Check static imports, dynamic discovery, registry
   entries, CI, Makefile, package scripts, docs, and validators. Zero static
   imports alone is not proof for `scripts/verify-ui`.
4. **Verify coverage.** For a test or checker, identify the live behavior it
   protects and the replacement coverage. If neither exists, keep it or wire
   it.
5. **Patch in a bounded batch.** Prefer a coherent group: one migration chain,
   one directory move, one doc consolidation. Give each file one owner.
6. **Run focused checks.** Run the closest affected tests, type checks, builds,
   link checks, and validators. Broaden only for changed shared surfaces.
7. **Record the outcome.** Update `docs/REPORT_CLEANUP_PLAN.tsv` for reports and
   evidence. Record Git recovery pointers for deleted historical material.
8. **Report.** State deleted, moved, wired, and retained items; checks run;
   checks not run; residual risk.

## Batch policy

- Keep batches reviewable. Do not mix a generated-artifact rewrite with a code
  move and a documentation deletion.
- Do not move test files and their paths piecemeal. Move a suite and update its
  runner in one change.
- Serialize checks that mutate the same runtime or database. Do not change
  tested source during a browser run.
- Do not regenerate a large ledger until the live source, discovery snapshot,
  and validator agree. If they do not, retire the ledger instead of growing it.
- Do not commit, amend, push, or open a PR unless explicitly requested.

## Builder verification direction

The purpose of verification is to find and prevent defects in the Builder, not
to maximize the number of reports or assertions. Retain a test when it proves a
current user-visible behavior, an independent data result, or a named safety
property. A reference count alone does not establish whether a verifier is safe
to delete.

The bounded Group slice in `scripts/verify-builder-group-slice.mjs` runs a TLA+
model, frontend schedules, a Go lifecycle test, and a browser case whose
captured trace is replayed. These checks have different scopes. The model does
not generate the frontend or backend cases, and the browser pass does not prove
all datasets or operation combinations.

Use the registry and the historical catalogue to group existing tests by the
behavior they protect. For each group, preserve unique assertions and source
oracles, run the smallest relevant implementation checks, and retire duplicate
tests only after the replacement has passed the required surface. Keep browser
and formal acceptance at explicit checkpoints rather than every inner-loop edit.
Record product, harness, and environment failures separately.

The Group-twice extension in `group-entry-raw-basic` currently runs after the
single-Group formal trace is written. It is not covered by that replay, and its
final table differs from the replay's restored-source outcome. Keep the
standalone Group-twice journey until a replacement preserves its behavior and
has passing browser evidence. Do not claim that the current Group model covers
two persisted Group steps; its validator accepts at most one.

Prove the selected checks can detect a real implementation defect. Keep source
and evidence integrity checks, negative validation, and independent oracles.
Use historical evidence only within its recorded source and fixture scope.

The Basic standalone coded-fields journey now checks selected labels through
the UI and exact per-patient values after Apply and reload. Its current-source
browser bracket passed after repairing stale test selectors and reading values
without their display-count badges. The selected-feature list now displays the
source, system, and version qualifiers already present in accessible names.
The semantic Specimen and expanded Coding Group workflows still need their
own CDA browser runs. Their local oracle and static checks are not CDA evidence.

The Basic Pivot-to-Unpivot journey passed its native browser bracket after the
driver was updated for the current row-intent dialog. The run checked the five
literal Observation rows, distinct missing and null categories, Pivot and
Unpivot previews, cancellation, Apply, editing, removal, reload, and source
restoration. The bracket reported source and API integrity as passing. This
result covers the synthetic Basic fixture; it does not establish CDA behavior
or runtime dataset identity. Summary:
`/private/var/folders/v_/j3zplgbs155cgwtqjftj377nd_4gyt/T/loom-verification-brackets/run-basic-pivot-unpivot-lifecycle-EdS5jL/summary.json`.

## Cleanup queue

These are the highest-value cleanup groups after the 2026-10-26 pass.

| Priority | Status | Group | Current evidence | Next action |
| --- | --- | --- | --- | --- |
| 1 | Done | Unrun Python tests in `scripts/tests/` | 49/49 tests pass; `make test-scripts` added; CI runs it; package audit regenerated | None |
| 2 | Pending | Local preview-index migration and artifact chain | One-off migration with a Makefile test hook; production replacement exists | Confirm the deployment window, then archive/delete as a unit |
| 3 | Pending | `scripts/verify-ui` test layout | Tests are split across colocated files, `helpers/tests`, and `maintenance/playwright/tests` | Plan one suite move with runner updates |
| 4 | Pending | Quarantined doc bundles | `docs/product/history/20260919-superseded` and `docs/architecture` have active validators | Move validators and pointers, then retire each bundle |
| 5 | Pending | Duplicate helpers | `shellQuote` in four places, `canonicalJSON` in four, `median` in three | Choose one owner per helper and migrate callers |
| 6 | Pending | Migration checker overlap | `check-playwright-migration.mjs` still guards Chrome/CDP patterns after the migration record retired | Fold the pattern guard into `check-native-playwright.mjs` or keep it as a small named guard |
| 7 | Pending | Orphan preview-index CLI | `local-preview-index-explain.mjs` and its test are separate from the live library import | Keep the library for `owned-query-probe.mjs`; retire the CLI/test if unused |
| 8 | Pending | Examples and experimental layout | Remaining examples are referenced; experimental runtime state is untracked | Document intentional samples and remove only unowned files |
| 9 | Pending | Historical sections in `docs/UI_VERIFICATION.md` | Historical baselines still sit beside current commands | Move historical sections to a dated archive or prune them |
| 10 | Done | Stale package inventory | 74 packages; `python3 scripts/package_audit.py` exits 0 and regeneration is idempotent | None |

## Acceptance criteria

- Every candidate in the current queue has a decision and evidence.
- No contract, inventory, or migration record has two active locations.
- Every deleted test or checker has a proven replacement or an explicit
  retirement decision.
- Every moved test-only module has its imports, runner paths, typecheck config,
  and focused tests updated.
- Orphan checks are either wired into a runner or deleted with replacement
  proof.
- Deleted reports and evidence have no active reader and a Git recovery pointer
  where history matters.
- Focused checks pass for every changed surface. Broader checks are run when a
  shared contract, runner, build, or package boundary changes.
- `docs/REPORT_CLEANUP_PLAN.tsv` and any authored worklist remain current.
- Unrelated work is preserved and no commit or publication occurs without an
  explicit request.

## Non-goals

- Product behavior changes, refactors, or feature work.
- Mass deletion by age alone.
- Rewriting generated evidence or historical runtime archives.
- Deleting quarantined archives while their validators still read them.
- Inferred Go package moves without package-audit evidence.
- Cleaning up uncommitted work owned by someone else.

## Completion

Declare completion only when the current queue is empty or every remaining item
has an explicit keep/quarantine decision with an owner and condition. Report the
files deleted, moved, merged, and wired; the checks run; the checks not run; and
the residual risk. A smaller repository is not evidence of a correct cleanup;
the preserved behavior and checks are.
