# B01: typed sources and truthful output contracts

Implementation checkout: `arch/integration`, based on `a921b9e5dca1d42a84a836286140fb3b4d704f3b`.
Status: focused and live gates passed; this report is not completion of B02–B08.

## Behavior

- Builder mutations require semantics version 3 and a closed, nested source payload. Old flat sources are read only through persisted-draft migration.
- Source edits retain column identity and presentation and use the existing compare-and-swap and command replay boundary.
- A related scalar projection records its first-resource policy. New publication requires acknowledgment; acknowledgment does not make it lossless.
- Output contracts distinguish scalar, array, and review-required structure. Scalar shape does not establish ML readiness.
- Compilation has an explicit resolved-input argument and digest. B01 has no external resolved values; B02/B06 populate this contract when their immutable references exist.
- Existing immutable receipts remain historical artifacts. New execution uses the current compiler contract rather than rewriting old receipts.

The existing source-type file owns these variants; no separate authoring package or wizard model was added. Selection references are introduced with B02, not exposed as an unsupported B01 operation.

## Local verification

All runtime checks used the disposable `loom-dev-arch-integration` stack (API 8182, UI 30002). Existing `loom-demo` and `loom-dev` were not changed.

- UI: 21 test files, 126 tests passed, including type and package-boundary checks.
- Driver and timing-probe tests: 12 passed.
- Backend/compiler/server/converter gate: 467 tests across 14 packages after review corrections; generated-boundary tests also passed. Acceptance suite: 27 passed.
- Production UI build passed; existing bundle-size warnings remain.
- OpenAPI generation consistency and dataframe package boundaries passed.
- The live Builder → Preview → Publish → Viewer → filter → CSV → reload journey passed 25 assertions in 11.3 seconds. It also proved publication rejection before related-selection acknowledgment, exact values after acknowledgment, lossless indexed/count columns, and retained warnings on lossy related values.
- Final full verification passed 32 assertions: frontend HMR 609 ms; backend rebuild 7,213 ms; recovery after deliberate build failure 6,903 ms. The combined full verification took 35,248 ms; that is separate from individual edit-to-assertion latency.

Detailed local evidence lives under `.artifacts/loom-dev/6d7df93d6a37/`. These artifacts are machine-local and are not committed as portable test results.

## Performance comparison

`scripts/measure-authoring-loop.mjs` runs one warmup and five interleaved baseline/candidate save → reconcile → preview samples. It restricts mutations to disposable verification explorers, checks identical fixture populations and literal rows, and rejects source changes during measurement.

The baseline is a detached `a921b9e5` checkout with the same three-Observation fixture. The first comparison measured median total latency of 47.40 ms baseline versus 44.41 ms candidate. Individual medians were save 8.17/8.47 ms, reconcile 23.70/24.43 ms, and preview 14.66/17.10 ms. These tiny warm-fixture samples show no threshold regression; they do not establish a general performance improvement or large-dataset throughput.

The final comparison after review corrections measured 41.53/40.28 ms total, 7.72/7.16 ms save, 20.78/19.72 ms reconcile, and 13.30/14.53 ms preview (baseline/candidate). The baseline and candidate populations and literal rows matched.

One fresh-project run exposed an incorrect test assumption: physical keys contain project/generation hashes, so legacy FIRST need not select the lowest FHIR ID. The backend ordering was preserved. Driver expectations now independently apply the ingestion-key contract and assert one exact value throughout the journey. Two observed project/key orderings are regression cases.

## Remaining product work

B01 does not yet provide saved populations, correlated concept editing, row-grain changes, temporal/unit policies, interpretation libraries, per-cell evidence, or pinned dataset artifacts. Those remain B02–B08. The full repository suite is scheduled at integrated B08; focused package and runtime gates apply here.
