# Preview and publish a dataframe

Preview must reflect the current authored draft. Cancel preserves saved state;
Apply saves the reviewed operation, and reload preserves its result. Publish
must activate the reviewed output and make its rows available in Viewer.

The matching real-browser cases are:

- `scripts/e2e/author-publish-view.spec.mjs`: review automatic Patient preview,
  apply Filter, compare saved draft identity across reload, publish, and verify
  the filtered fixture rows in Viewer and the downloaded archive.
- `scripts/e2e/group-pivot.spec.mjs`: Group with Count rows matches literal
  Observation inputs; Pivot preserves Cancel/Apply behavior and saved rows
  through reload.
- `scripts/e2e/related-field.spec.mjs`: expand related Observation rows, select
  a field from the current related record, reject ambiguous ONE, and preserve
  all distinct values with ALL after Apply and reload. Cancel preserves the
  saved Group.

Run the closest spec using [verify](../SKILL.md). A proposed preview does not
prove durable Apply, and a successful publication call does not prove Viewer
rows. For physical ClickHouse, registry, and GraphQL checks, follow
[real-data acceptance](../../../../docs/REAL_DATA_ACCEPTANCE.md).
