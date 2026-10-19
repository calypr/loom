# B07 cell and publication evidence verification

## Implemented boundary

Targeted cell explanations are compiled from the same immutable receipt and
pre-reduction physical expression as the published value. The endpoint is
receipt, project, generation, authorization-scope, output, row, and column
bound. Contribution pages are capped at 100 records and ordinary Preview does
not collect provenance.

The Viewer distinguishes populated values, no matching record, recorded null,
ambiguous matches, invalid types, incompatible units, and incomplete witness
searches. Repair actions use the server-owned authored feature descriptor and
return the Builder to the exact output, occurrence, and feature.

Publication quality runs while the candidate materialization stream is being
consumed, separately from bounded Preview. Reports bind receipt, project,
generation, scope, output, and policy version and record:

- scan completeness and configured row/key limits;
- feature coverage, recorded nulls, and empty arrays;
- stable-key missing/duplicate counts;
- ambiguity, invalid-type, and incompatible-unit failures; and
- explicit omissions when cancellation, deadline, or another interruption
  prevents natural exhaustion.

Incomplete or failed evidence is retained on the failed candidate execution.
Only complete passing reports can cross the atomic publication activation
boundary.

## Executable evidence

Verified at `8c534c69`:

- `go test ./internal/explorer/... ./internal/dataframe/execution/... ./internal/dataframe/compiler/... ./internal/dataframe/publication/... ./internal/server -count=1`
- `make openapi-check`
- `npm --prefix ui test -- --run` — 162 tests passed
- `node --test scripts/measure-b07-evidence.test.mjs` — 6 tests passed
- `make verify-fast` — passed against isolated project
  `loom_dev_verify_mu7mlbjg-026653af`

The live browser journey proves the published MAX value `180` against exact
Observation contributors `172.5` and `180`, distinguishes a Patient recorded
null, follows its repair action into the exact Builder feature, returns to the
Viewer, and completes the existing publish/filter/export/reload journey.

Performance evidence is retained at
`.artifacts/loom-dev/6d7df93d6a37/b07-performance.json`:

| Measurement | Result |
| --- | ---: |
| Preview with tracing unused | 19.19 ms median |
| One targeted cell trace | 15.00 ms median |
| Preview plus trace | 34.11 ms median |
| Fresh full publication and quality scan | 82.97 ms |
| Quality evidence | 1,450 bytes |
| API RSS | 10.4 MiB maximum |

All trace pages were bounded and the targeted loop remained below 40 ms on
the retained fixture. These are absolute same-machine measurements, not a
claim of improvement over a historical baseline.

## Failure-path acceptance proof

The deterministic live fault driver now exercises both required failures
through the real API and then reads the active dataframe through GraphQL:

- an intentionally incomplete full quality scan returned HTTP 503 and left
  the prior Explorer revision and execution active;
- an injected release compare-and-swap loss returned HTTP 409 with
  `PUBLICATION_ACTIVATION_CONFLICT`; and
- after each failure, GraphQL returned the same execution identity, two rows,
  columns, count, and row digest as the baseline publication. Neither response
  exposed the candidate receipt, rows, counts, or quality report.

The first corrected live run exposed a real split visibility bug: the bundle
pointer moved before the Explorer release transaction failed, so GraphQL read
the rejected candidate. The published reader now treats the active project
release's exact selector-to-execution binding as authoritative. The focused
regression first returned `execution-b` while release A was active, then passed
with `execution-a` after the fix.

Evidence is retained at
`.artifacts/loom-dev/6d7df93d6a37/mu7o7ayb-924b071b/b07-fault-verification.json`.
The open CDA-FHIR fixture was also loaded from `CDA-FHIR/META`, and
`verify-current` passed for `loom_dev_cda_fhir` with browser evidence under
`.artifacts/loom-dev/6d7df93d6a37/current-mu7pbze9/`.
