# Read and export a published dataframe

`tests/e2e/author-publish-view.spec.mjs` publishes the fixture's authored
female-Patient filter, opens Viewer, compares visible rows with fixture data,
and downloads a ZIP through `Download dataset` and `Download ZIP`. It checks
archive manifest version 2, included Patient identity, and exclusion of rows
removed by the authored filter.

Run it through the target-bound command in [verify](../SKILL.md). This case
covers the authored filter. It does not establish that an interactive Viewer
facet leaves the full authorized export unchanged, arbitrary datasets, or every
archive/schema combination. Those claims need separate explicit assertions.
Physical table and GraphQL guarantees belong to
[real-data acceptance](../../../../docs/REAL_DATA_ACCEPTANCE.md).
