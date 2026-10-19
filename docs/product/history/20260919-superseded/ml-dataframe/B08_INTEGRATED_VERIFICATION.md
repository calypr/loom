# B08 pinned artifact and integrated verification

## Outcome

B08 is runtime-proven on the isolated local Docker stack. Loom prepares a
bounded server-side ZIP from the exact published execution and output shown to
the researcher, holds a renewable execution read pin while streaming, commits
the archive atomically, and lets the browser hand the completed response to
its native download manager. The browser no longer buffers the training
dataset to assemble the primary artifact.

The archive contains `data.csv`, `schema.json`, `provenance.json`,
`quality.json`, `README.md`, and `manifest.json`. The manifest binds the exact
project, generation, receipt, execution, output, and Explorer revision and
records member sizes and SHA-256 digests. Null and array encodings are declared
in both schema and manifest metadata.

## Hostile live cases

The live B08 driver ran two deterministic no-auth-only fault scenarios through
the production HTTP surface:

- Export A started from execution `b51b3f5c-f679-4d22-97e1-ed36d0f27503`.
  Publication B completed at `01:36:53.858Z`, while export A completed at
  `01:36:58.200Z`. The active execution advanced to
  `2a7d6714-86b5-4ff9-9438-b6c21375f489`, but the downloaded manifest retained
  execution A and revision A. Its two rows, nine features, member checksums,
  archive digest, and response digest header all matched.
- A one-row preparation limit failed while streaming the second row. The API
  returned `ARTIFACT_PREPARATION_FAILED`; downloading the deterministic
  artifact ID returned `ARTIFACT_NOT_COMPLETE`; and repeating the same
  idempotency key returned `ARTIFACT_FAILED_RETRYABLE`. No complete ZIP was
  visible.

The second scenario found a real filesystem-store defect: an unexpired failed
record was being deleted and retried under the same idempotency key. The store
now returns that immutable failed record; a new key is required for a new
attempt.

Evidence is retained at:

- `.artifacts/loom-dev/6d7df93d6a37/mu7o7ayb-924b071b/b08-artifact-verification.json`
- `.artifacts/loom-dev/6d7df93d6a37/mu7o7ayb-924b071b/b08-concurrent-artifact.zip`
- `.artifacts/loom-dev/6d7df93d6a37/report.json`
- `.artifacts/loom-dev/6d7df93d6a37/mu7pyznh-a2d9297d/`

## Integrated gates

- `go test ./... -count=1` passed with loopback enabled.
- `make openapi-check graphql-check dataframe-boundaries` passed.
- `npm --prefix ui test -- --run` passed 25 files and 164 tests.
- `npm --prefix ui run build` passed the package and demo production builds.
- The four development-driver test files passed 29 tests.
- `make verify-full` passed 76 assertions through Builder, Preview, Publish,
  Viewer, explanation, repair, filter, native artifact download, reload, hot
  reload, and failed-build recovery.
- The open CDA-FHIR fixture loaded from `CDA-FHIR/META`, and `verify-current`
  passed for `loom_dev_cda_fhir`.

## Measured local behavior

The final same-machine run downloaded the 8,466-byte artifact in 216 ms. Vite
hot reload took 615 ms, backend hot reload took 5.980 seconds, failed-build
recovery took 6.635 seconds, and the complete 76-assertion browser scenario
took 23.108 seconds. The concurrent export proof prepared an 8,466-byte ZIP
with two rows and nine features.

These measurements establish bounded behavior and the sub-30-second iteration
loop. They are not claimed as gains over a historical export baseline.

## Deferred product capabilities

Parquet, train/validation splitting, imputation, arbitrary formulas, SUM/AVG,
expand-to-many row authoring, and model training remain explicit follow-up
work. B08 exports the checked dataframe and its evidence; it does not silently
invent those policies.
