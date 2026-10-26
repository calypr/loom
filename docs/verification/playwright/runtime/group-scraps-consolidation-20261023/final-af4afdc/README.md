# Group scraps consolidation

This record closes the duplicate Group ALL-cell population checks and two native journeys on tested source commit `af4afdc56ed8b9a2a5d137c3cd328e40c3cb8ff8`.

The source change retires two Group-related MJS fragments and moves their complete-population multiset checks into the existing `cda-row-oracle`. The comparison preserves duplicate rows and duplicate values inside ALL cells, ignores row order and ALL item order, and rejects missing or extra population. All four registered CDA caller checkpoints remain attached to the shared oracle. The bounded-preview API and its array-order semantics did not change.

Two product fixes support the full journeys. Related-column selection now has an explicit Preview action that stages a proposal for Cancel or Apply; the existing atomic Add action remains unchanged. Generic Add columns after a terminal Group now appends a new step, while explicit history Edit still opens the saved Group contributor editor.

The catalogue retains its historical 678-record baseline while current tracked MJS sources total 676. The filename inventory reviewed 67 Group-named records: 2 retired and 65 retained. Other Group families remain independent, including PRESENCE, repeated expansion, population removal, collection replacement, composite filters, downstream compositions, and Patient ONE failure preconditions. These passes do not establish every Group aggregate or workflow.

## Native results

| Journey | Result | Checks | Timing |
| --- | --- | ---: | --- |
| Owned CDA `group-edit-before-related-column` | Passed | 16/16 | 28 render checkpoints; maximum 4,081 ms of a 5,000 ms budget |
| Fresh authored `builder-authoring/group-entry` | Passed | 19/19 | 1 render checkpoint at 1,350 ms; maximum action 1,404 ms |

The CDA run checked the independent raw Observation status multiset, staged related-column Preview/Cancel/Apply, saved Group identity, reload, removal Cancel/Apply, and final source restoration. The authored Group run performed ordinary field Add, empty-key COUNT_ROWS, keyed Group edits, Cancel, Apply, reload, and removal.

The authored trace has 29 observed actions. `BuilderJourney` replay passed one trace with 31 states, 30 distinct states, depth 30, zero invalid traces, and zero unmodeled events. Its journey-only self-test passed 24 controls and reported one explicit skip: this capture did not observe a `LatePreviewErrorIgnored` event paired with a later Preview from the same request attempt. That schedule remains model-only.

The docs closeout is a separate documentation-only commit after the tested source commit. `closure.json` records source identity, run outcomes, TLA traces, self-tests, and artifact hashes. Each raw native report that is no longer tracked has a recovery command that restores its exact bytes from the base commit. The docs commit does not update product source pins or generated test metadata.

## Recorded corrections and remaining gaps

Earlier iterations also corrected saved Row definition settings closure, the `Other fields` role=tab target, and selection inside collapsed route alternatives. The closure summarizes the two final failed CDA attempts. Restore the first report's exact bytes from the base commit with this command:

```sh
git show a6ab20357b969370d7f2da444f713942367f62ff:docs/verification/playwright/runtime/group-scraps-consolidation-20261023/final-af4afdc/cda-group-related-source/failed-preview-reader/native-report.json > /private/tmp/failed-preview-reader-native-report.json
```

The first attempt treated the array count badge as part of the cell value. The second found a related step inside a collapsed Column adjustments disclosure. The current driver reads the value span separately, checks the array badge against the oracle, and opens the disclosure through its native summary control.

The full `BuilderWorkspace.addColumnsSource` component file still has three failures that were reproduced on the clean base: one shared-source inventory expectation, one missing Member status menu option, and one missing related-source search box. The focused Group additions passed 4/4 and `PreviewTable.unit.test.ts` passed 44/44. `npm run typecheck:tests` also retains existing diagnostics; no new changed-line type diagnostic was found. The native-verification-bracket suite has the same SIGINT startup failure on base and candidate (63/64 baseline, 64/65 candidate). These are recorded gaps, not full-suite passes.

See [closure.json](closure.json) for the exact outcomes, source fingerprints, preserved failures, and retained artifact hashes. The separate historical `group-entry-raw-basic` proof at source `1918824fe1eb3ceb3a24f7b93249d0c5faa963e3` remains available under `builder-group-entry-20261023/final-191882`.
