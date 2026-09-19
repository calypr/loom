# Loom development verification map

This map covers the synthetic local workflow, not every Builder or Viewer
feature. Read the matching recipe before changing a scenario.

## Preconditions

- Run `make dev`, then require `make dev-doctor` to succeed.
- The default target is `loom-dev`, UI port 3180 and API port 8180. Never
  substitute the canonical `loom-demo` stack or its ports.
- Each browser run owns a fresh synthetic project. The stable manual fixture
  is separate. Retain run projects until an explicit development-volume purge.
- Run one verification command at a time. Do not edit source during full probes.

## Proof rules

Run `make verify-fast` for the complete browser scenario. Read its report and
linked DOM and training-artifact evidence. Failed or skipped assertions are not passes.
Setup uses ingestion APIs. Authoring, preview, publication, filtering, and
download use the real UI. Independent GraphQL reads check the stored result.

Generated column names vary. Match returned lineage to literal expected
values. Do not replace browser assertions with API-only checks.

## Features

- [Author a dataframe](authoring.md).
- [Preview and publish](publication.md).
- [Explain, filter, and export](viewer.md).
- [Iterate on source](iteration.md).

## Not covered

Authentication, real NCPI scale, pagination, charts, and every possible FHIR
interpretation remain outside this map. The fixture does cover multiple
Observations per Patient, deliberate reduction, immutable interpretation
revisions, publication-quality evidence, targeted cell explanation, repair
focus, and exact training artifacts. Add a real user path and literal expected
outcome when extending coverage.
