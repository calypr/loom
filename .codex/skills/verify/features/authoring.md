# Author a dataframe

A researcher creates an Explorer and a `New table`, chooses its starting
records, selects fields, and reviews rows before applying changes. The starting
record section is labeled `Starting record type`; field actions include
`Use gender as filter`. Keep available actions and fields browsable.

Run these cases with the target variables documented in [verify](../SKILL.md):

- `scripts/e2e/root-change.spec.mjs`: changing Patient starting records to
  Observation offers `Keep current rows` and `Apply row change`. Cancel keeps
  the draft digest; Apply persists Observation rows after reload.
- `scripts/e2e/record-network.spec.mjs`: Patient→Observation follows literal
  fixture references through related-row expansion and reload.
- `scripts/e2e/author-publish-view.spec.mjs`: create the table through native UI,
  select family/gender fields, compare preview values with fixture records,
  apply a gender filter, and reload the saved draft.

The test owns a fresh Explorer. Wait for saved state and settled previews;
rendered choices alone do not prove selection or persistence. Compare row
multiplicity with fixture references: Patient 001 has two related Observations,
not one. Scope cleanup to the run-owned Explorer.
