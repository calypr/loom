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
`Download training artifact`. Reload the page.

## Driving it with loom-dev

Preconditions: the run has published both fixture Patient rows.

- Run `make verify-fast`. The driver chooses `Viewer` through the header and
  requires the published table, not just a runtime API response.
- It chooses `Load values` and the checkbox associated with `female`.
  Only `dev-patient-001` must remain in the table.
- It opens a cell explanation and requires the exact two Observation IDs and
  values used by the authored reduction. It follows the server-owned repair
  target to the exact Builder feature and returns to Viewer.
- It clicks `Download training artifact` and parses the ZIP. The archive must
  contain `data.csv`, `schema.json`, `provenance.json`, `quality.json`,
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
