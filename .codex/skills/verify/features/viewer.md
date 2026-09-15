# Filter and export a dataframe

A researcher narrows published rows, downloads a CSV, and reloads without
losing the selected Explorer or Viewer mode.

## Sub-features

- `viewer-selected` opens the Explorer just published in Builder.
- `viewer-filter` selects female and excludes the missing-gender Patient.
- `viewer-csv` exports visible columns and the filtered row.
- `viewer-reload` preserves Explorer identity and Viewer mode across reload.

## How to get to it (user POV)

Choose `Viewer` after publishing. Load the gender filter's values, select
`female`, and choose `Download CSV`. Reload the page.

## Driving it with loom-dev

Preconditions: the run has published both fixture Patient rows.

- Run `make verify-fast`. The driver chooses `Viewer` through the header and
  requires the published table, not just a runtime API response.
- It chooses `Load values` and the checkbox associated with `female`.
  Only `dev-patient-001` must remain in the table.
- It clicks `Download CSV` and parses the file. Headers must match visible
  physical column IDs. The row contains `dev-patient-001`, `Example`,
  `Example-Smith`, `172.5`, and `2` in runtime column order.
- It reloads the actual browser URL. Viewer and published data must remain
  available. Inspect the post-reload DOM evidence.

## Gotchas

- A checkbox label can associate through `for`, not a nested input.
- CSV headers use physical names, not display labels. Keep returned lineage.
- Reload persistence does not claim preservation of an unsaved filter.
- Do not manufacture a corrected URL during the reload assertion.
