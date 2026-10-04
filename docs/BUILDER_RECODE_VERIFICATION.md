# Column recoding verification

Exact-category recoding is available from Preview and configure → Columns. Users no longer need to open Advanced source setup. Draft inputs survive unrelated rerenders; changing the saved transformation or column resets the keyed draft without a synchronization effect. Commands retain the current snapshot, draft, output and resolved column capability ownership.

The registered basic fixture case passes all eight required checks: two independent Patient IDs remain distinct under raw ALL, both recode to one shared category before reduction, ALL→ONE→ALL and reload preserve the transformation, and Remove restores both raw IDs after reload. Advanced source setup remains closed. Maximum recorded action: 690 ms; errors: none.

```sh
LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-construction-implementation LOOM_DEV_COMPOSE_PROJECT=loom-dev-6d7df93d6a37 LOOM_DEV_API_PORT=8188 LOOM_DEV_UI_PORT=30008 LOOM_DEV_PROJECT=loom_dev_c89a69d7e137 node scripts/verify-ui/builder-authoring.mjs --case cohort-recode --report /tmp/loom-cohort-recode-ordinary-columns.json
```

Report: `/tmp/loom-cohort-recode-ordinary-columns.json.cohort-recode`. Source fingerprint: `42482158c733a19e0dc66cebaf534a4a9f58a130bef0a74cf5f308169b7a9ff4` (1122 files).

The CDA follow-up passes 45 timed checks, maximum 2287 ms, with errors[] and unchanged source/API build. Native Save/Remove, ALL/ONE edits, Undo and reload preserve independently scoped Specimen member values and stable bindings. A raw ONE disagreement retains the saved ALL table. Receipt-bound CellTrace API checks supplement native row inspection; they do not prove a native draft-cell Explain control.

```sh
LOOM_COHORT_ROW_VALUE_CASE=transformed-category node scripts/verify-cda-cohort-row-sources-browser.mjs /tmp/loom-cda-cohort-recode-menu-contained-select
```

Report: `/tmp/loom-cda-cohort-recode-menu-contained-select/report.json`. The native select helper dismisses its platform popup inside the Columns menu to avoid destroying the draft through an outside click. Other callers retain their existing dismissal behavior.

Focused editor and table tests pass 49/49; UI test typechecking passes. This proof covers exact string member recoding, not every transformation family or the remaining Builder workflows.
