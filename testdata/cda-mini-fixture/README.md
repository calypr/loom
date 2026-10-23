# CDA mini fixture

A small, schema-faithful FHIR fixture derived from the CDA data shapes in
`CDA-FHIR/META` and `META_SMALL`. The resource identifiers and values are
synthetic; no patient records are copied.

The fixture exists to exercise the Builder on realistic CDA relationships and
nullable fields without loading the full CDA dataset. It is small enough to run
in CI.

## Contents

| Resource | Rows | Purpose |
| --- | ---: | --- |
| Patient | 6 | CDA identifiers, birthsex extensions, study membership; no names or genders |
| ResearchStudy | 2 | Two cohorts with CDA identifiers |
| ResearchSubject | 7 | Links patients to studies; patient 001 belongs to both studies |
| Specimen | 6 | Tumor and Normal specimens; one parent/child specimen pair; patient 006 has none |
| Observation | 10 | Quantity, coded, string, component, and missing-value observations |
| Group | 2 | Person groups over the six patients |

## CDA shape

Patients use the same extension and identifier systems as the source data:

- `part-of-study` extension references a `ResearchStudy`.
- `us-core-birthsex` extension supplies `F`/`M`; patient 006 omits it.
- `https://cda.readthedocs.io/subject_id` is the primary identifier.
- `https://portal.imaging.datacommons.cancer.gov/PatientID` and other CDA
  identifier systems appear as secondary identifiers.
- Patients intentionally have no `name`, `gender`, or `birthDate` fields.

Observations use CDA-style codes for days between birth and diagnosis, stage,
and diagnostic method. They include quantity, codeable-concept, string, and
component values. Two observations omit a value to exercise nullable output.

## Relationships

- Patient 001 has two research subjects, one per study.
- Patient 001 has two specimens: a Tumor and its Normal child specimen.
- Observations 001 and 002 focus on patient 001's Tumor specimen.
- Patient 006 has no specimen.
- Group 001 covers patients 001–003; Group 002 covers patients 004–006.

## Loading

Point the dev harness at the fixture directory:

```sh
LOOM_DEV_FIXTURE_DIR=$PWD/testdata/cda-mini-fixture make dev
```

The bootstrap table contains only `id` because the CDA patients carry no
`name` or `gender` candidates. Builder tests should add CDA columns such as
`identifier[].value`, `identifier[].system`, birthsex, study, specimen type, or
related observation values through the UI.

## Builder journeys

The mini profile is wired into five journeys:

- `load-preview`
- `add-column-preview`
- `filter-preview`
- `group-twice-preview`
- `cda-mini-birthsex-filter` (adds the birthsex extension and filters to the three female patients)

Run them against a running development stack:

```sh
bash scripts/run-cda-mini-journeys.sh
```

The script sets `LOOM_CDA_MINI_FIXTURE=1`, points `LOOM_DEV_FIXTURE_DIR` at this
directory, and selects `verification/builder-interactions/cda-mini-journeys.json`.
Reports and traces land in `.artifacts/builder-journey/cda-mini/`.

The GitHub Actions workflow `.github/workflows/builder-cda-mini.yml` runs the
same five journeys.

The retained CDA Group edit/remove/reload branch is a manual-only entry in the
same journey manifest. It has not been run and is not part of the fixed journey
list above or CI automation. With the owned development stack running on its
published loopback ports, invoke it directly through the existing runner:

```sh
LOOM_JOURNEY_DATASET=mini \
LOOM_DEV_SOURCE_ROOT="$PWD" \
LOOM_DEV_COMPOSE_PROJECT=loom-dev-fcbf4d7d11f4 \
LOOM_DEV_PROJECT=loom_dev_cda_manual \
LOOM_DEV_GENERATION=fixture-v1 \
LOOM_DEV_HOST=127.0.0.1 \
LOOM_DEV_API_PORT=8180 \
LOOM_DEV_UI_PORT=3180 \
LOOM_DEV_FIXTURE_DIR="$PWD/testdata/cda-mini-fixture" \
node scripts/run-builder-journey.mjs \
  --manifest verification/builder-interactions/cda-mini-journeys.json \
  --journey edit-remove-reload-cda-manual \
  --trace .artifacts/builder-journey/cda-mini/edit-remove-reload-cda-manual-trace.json \
  --report .artifacts/builder-journey/cda-mini/edit-remove-reload-cda-manual-report.json
```

The manual entry sets no native case and selects the edit/remove contract, so
the runner invokes the retained Playwright branch without imposing native
`EditGroupAgain` trace requirements. `LOOM_JOURNEY_DATASET=mini` is required to
select its CDA extension-field assertions. Its removal and reload checks verify
the restored row/column counts and visibility of the first two source IDs; they
do not compare every raw source row. This branch remains unrun and manual-only.

Regenerate the deterministic NDJSON files with:

```sh
node scripts/fixtures/generate-cda-mini-fixture.mjs
```

