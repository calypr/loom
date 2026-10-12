# Explain, filter, and export a dataframe

A researcher inspects why a value exists, follows a repair action when needed,
narrows published rows, downloads an exact training artifact, and reloads
without losing the selected Explorer or Viewer mode.

## Sub-features

- `viewer-selected` opens the Explorer just published in Builder.
- `viewer-filter` selects female and excludes the missing-gender Patient.
- `viewer-evidence` explains a value using the exact contributing FHIR records.
- `viewer-repair` returns to the exact authored feature and back to Viewer.
- `viewer-artifact` exports the entire pinned publication with definitions,
  provenance, quality evidence, schema, and checksums.
- `viewer-reload` preserves Explorer identity and Viewer mode across reload.

## How to get to it (user POV)

Choose `Viewer` after publishing. Explain a value and inspect its source
details. Load the gender filter's values, select `female`, and choose
`Download dataset`. Reload the page.

## Driving it with loom-dev

Preconditions: the run has published both fixture Patient rows.

- Run `make verify-j05`. The driver chooses `Viewer` through the header and
  requires the published table, not just a runtime API response.
- It chooses `Load values` and the checkbox associated with `female`.
  Only `dev-patient-001` must remain in the table.
- It opens the Basic Patient family-cell explanation and checks the fixture
  evidence (`Example` and `dev-patient-001`). This recipe does not exercise
  the conditional repair action; do not count repair as covered by this pass.
- It clicks `Download dataset`, opens the dataset modal and follows `Download ZIP`.
  The archive contains `data.csv` or typed `data.jsonl`, according to its format,
  plus `schema.json`, `provenance.json`, `quality.json`,
  `README.md`, and `manifest.json`; manifest identity and member checksums must
  match, and the data must contain both published Patient rows. Viewer filters
  do not mutate the pinned training dataset.
- It reloads the actual browser URL. Viewer and published data must remain
  available. Inspect the post-reload DOM evidence.

## Gotchas

- A checkbox label can associate through `for`, not a nested input.
- CSV headers inside the artifact use physical names, not display labels. Keep
  the schema and provenance sidecars with the data.
- The primary training export is server-built and exact-revision pinned. Do
  not reintroduce browser pagination and whole-dataset buffering.
- Reload persistence does not claim preservation of an unsaved filter.
- Do not manufacture a corrected URL during the reload assertion.

## Registered filter and export lifecycle

```bash
node scripts/run-native-verification-bracket.mjs --scenario viewer-query --case dataset-zip-filtered-complete-population --target-from-environment --grep 'Viewer dataset ZIP exports the complete authorized population while a facet is selected$'
```

Load the validated owned environment before this environment-bound command.
The case uses a fresh Basic project. It verifies a one-row visible facet while
the pinned ZIP contains the complete two-Patient authorized population, typed
values, modal Cancel/reopen, reload and repeat download. It does not prove
external authorization or the conditional Builder repair route.

The real-CDA publication recipes in [publication.md](publication.md) exercise
Viewer row rendering and reload; their passes do not substitute for this
filter/export/explanation lifecycle. Historical exact reports remain in the
coverage ledger and must retain their recorded scope.

2026-10-11 maintenance: the registered Basic filtered-population ZIP case
passed 10/10 checks in 28.1 seconds, with source/API integrity and after-health
passing. Maximum measured action was 580 ms. This proves the recorded
filter/export/reload scope; cell repair and external authorization remain unproved.
