# Report cleanup plan

Inventory: tracked files at commit `ad531a0b6` on `feature/deep-clean`.
[The file-by-file table](REPORT_CLEANUP_PLAN.tsv) contains a proposed disposition,
reason, known consumers, and the work needed before each change. It is a plan;
the `state` column records changes made after the inventory.

The initial deletion pass removed all 24 files proposed for deletion, plus the
obsolete MJS catalog decision log. Its unique historical comparison and Group
closeout context are summarized in `docs/BUILDER_JOURNEY_AUDIT_GOAL.md`.

A second pass removed 55 obsolete runtime report and closure files and the
superseded 2026-10-04 migration decision log. Each removed runtime file belonged
to a complete report/closure pair or a standalone record with no reader outside
the historical freeze snapshots. The 138 coverage rows were identical before and
after the runtime pass, so no case status or freshness changed. The migration log
was retired to `docs/PLAYWRIGHT_TEST_MIGRATION.md` with a Git recovery pointer.

A third pass removed two historical diagnostic report/closure pairs whose only
remaining references were worklist evidence cells. Those cells now carry the
`ad531a0b6` Git path and SHA-256, and the 138 coverage rows were unchanged. Two
other candidate reports were restored after the coverage output showed their
removal changed the selected evidence for two CDA rows.

| Recommendation | Files | Tracked text lines | Meaning |
| --- | ---: | ---: | --- |
| Delete | 24 | 45,781 | Remove after the row's consumer and recovery conditions are satisfied. |
| Merge | 210 | 99,981 | Carry useful information into the named or described current record and retire the separate file. |
| Keep | 103 | 91,137 | Needed by a current verifier, fixture, authored contract, or active record; reconsider when its consumer changes. |

Counts and line totals describe the original inventory, not expected savings. Compressed files
are counted by bytes and have no text line count. Merging may retain some lines.

## Conversion ledger finding

The source-conversion ledger no longer represented the live registry. The
checker reported 524 issue lines; 104 registry cases were absent from the ledger
and 19 present cases disagreed. The discovery snapshot declared 169 main tests
while current `playwright --list` reports 254, and 20 recorded spec hashes were
stale. Regeneration would have enlarged a historical artifact rather than
removed bloat. The ledger, checker, builder, tests, discovery and inventory
snapshots, and preimage ledger were retired; historical copies remain
recoverable at `ad531a0b6`. `check-native-playwright.mjs` and the registry
tests are the current gates.

## Scope and confidence

The table covers all 269 tracked files in `docs/verification/playwright/runtime`,
other identified report and evidence files under `docs/verification` and
`verification`, UI and package audit inventories, dated audit and product-history
reports, and report-shaped test fixtures. It excludes executable code, TLA
models, product schemas, ordinary documentation, and fixture inputs that are not
reports. The inventory was built from tracked paths, name and directory searches,
and source references; unusual reports that lack those signals could be missed.

These are proposed end states, not proof that each deletion is immediately safe.
For runtime evidence, the table uses named references and known directory reads
to distinguish likely disposable history from active machine inputs. Internal
cross-references between runtime files have not all been traced. In particular,
`coverage-status.mjs` can scan every immediate JSON in a supplied directory,
and a browser-evidence test copies a journey directory tree. A missing literal
reference therefore does not establish that a file is unused. Recheck each group
and its complete consumers before changing it.

## Order of work

1. **Generated snapshots and duplicate presentations.** Untrack reports that
   have no reader, change writers to use ignored local output, and keep their
   generation commands. Check source pins, CI paths and documentation first.
   The generated UI interaction inventory and formal report pair are candidates.
2. **Superseded migration and audit history.** Carry any still-current decisions
   into the active record, retain a Git recovery pointer, and remove separate
   snapshots once dependent links and migration checks are updated. The
   Playwright conversion ledger was evaluated and retired as historical
   migration data; see Conversion ledger finding.
3. **Browser runtime reports and closures.** Establish which facts drive current
   coverage, freshness, and retained-evidence checks. Consolidate only when the
   replacement preserves the relevant results and distinctions; move tests to
   small representative fixtures where appropriate. Adapt directory scanners,
   exact references, and hash bindings together. Keep original historical bytes
   in Git history and preserve any evidence that remains an active input.
4. **Current inventories and large fixtures.** Keep the useful checks and
   authored inputs, but remove duplicated fields or replace oversized fixtures
   only after demonstrating equivalent behavior. The registry and test code
   themselves are outside this report inventory and need a separate review.

For each completed group, compare current coverage and freshness results, run
its affected verifier and negative controls, and report any retired guarantee
explicitly. Generated output is not evidence that a test executed. Browser and
formal lifecycles require their own verification when their behavior changes.
