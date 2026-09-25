# Construction preview performance baseline

## Status

The latest validated 10-sample run completed at 2026-09-25 01:58 UTC against API revision `7df9bf029` and the integrated local fixture. Its report is `/private/tmp/construction-preview-phase-timing/2026-09-25T01-58-07-216Z/report.json`. The one-sample browser smoke report is `/private/tmp/construction-preview-phase-smoke/2026-09-25T01-57-45-928Z/report.json` and is excluded from the distribution.

All 10 samples passed exact comparison rows, selected-input checks, candidate row count, enabled Confirm, `proposalId`, and pre-action BuilderState identity checks. Each produced 3 candidate rows from 2 base rows and 35 changed comparison cells across five examples. There were zero canceled or failed API requests at concurrency 1. The earlier 2,692.4 ms pilot and failed identity diagnostics are excluded.

| Client label | Samples | Median | p95 |
| --- | ---: | ---: | ---: |
| Cold client | 1 | 1,810.8 ms | 1,810.8 ms |
| Warm client | 9 | 1,561.1 ms | 1,946.3 ms |

The cold lane has one observation, so its p95 is that single sample. “Warm” means the same Chrome profile was reloaded between later previews; it does not establish backend cache state.

The measured API request durations show `table-shape-proposals` as the longest endpoint on the critical path. Capability and semantic-inventory requests overlap; resolution completes before the proposal call begins.

| API endpoint | Samples | Median | p95 |
| --- | ---: | ---: | ---: |
| `table-shape-proposals` | 10 | 791.1 ms | 837.9 ms |
| `table-shape-resolutions` | 10 | 344.0 ms | 436.6 ms |
| `table-shape-capabilities` | 10 | 363.2 ms | 525.2 ms |
| `semantic-inventory` | 8 | 191.8 ms | 377.4 ms |

Server-side `Server-Timing` phases from the same ten proposal requests:

| Phase | Samples | Median | p95 |
| --- | ---: | ---: | ---: |
| Candidate compile and receipt verification | 10 | 195.2 ms | 284.1 ms |
| Base preview query | 10 | 5.5 ms | 12.8 ms |
| Candidate preview query | 10 | 6.5 ms | 23.1 ms |
| Row diff | 10 | 0.018 ms | 0.025 ms |
| Cell trace evidence | 10 | 73.9 ms | 94.1 ms |
| Receipt and exclusion evidence | 10 | 1.4 ms | 2.1 ms |
| Total comparison | 10 | 207.7 ms | 230.6 ms |

Comparison time includes the nested preview, diff, and evidence phases. The two preview queries and in-memory row diff are small; cell trace is the largest measured comparison subphase. The proposal request median is 791.1 ms, while candidate compilation plus the full comparison account for about 403 ms at their respective medians. The remaining request time is outside those phases, including base-context loading and candidate construction; it has not yet been timed separately. One A/B experiment is to reuse the validated base receipt/context from the immediately preceding resolution for the proposal when the complete project, Explorer, snapshot, draft, output, and authorization binding matches, compared with the current reload-and-compile path. Measure the full proposal and action-to-row distributions and retain reuse only if it improves latency without changing exact rows or identity checks.

The initial pre-instrumentation distribution completed at 2026-09-25 01:27 UTC and is recorded at `/private/tmp/construction-preview-artifacts/2026-09-25T01-26-57-199Z/report.json`. It had warm-client median/p95 1,434.7/1,777.7 ms and proposal API median/p95 688.9/787.0 ms. Its resource-timing classifier mislabeled proposal paths as context-resolution because `/explorers/` matched first; the current harness classifier is corrected.

## Fixture and workload

The checked local stack used UI `http://127.0.0.1:30006`, API `http://127.0.0.1:8186`, project `loom_dev_c89a69d7e137`, Explorer `loom-dev-bootstrap`, and generation `fixture-v1`. The read-only Builder doctor passed with HTTP 200 and a ready V2 Builder state. Its report is `.artifacts/loom-dev/c89a69d7e137/report.json` in the integration worktree.

The fixture has two Patient rows and three authored text columns: `id`, `name[].family`, and `gender`. A direct, read-only table-shape capabilities request returned HTTP 200, listed Unpivot as supported, and marked derived calculation unsupported because the compiler output has no scalar numeric columns. The Calculate path was abandoned before a proposal was issued.

The browser selected Unpivot over `id` and `gender`, with null rows dropped, proposed the change, and rendered an applicable comparison. It did not click Confirm or Apply. The comparison shows the two original Patient rows removed and three new rows: `dev-patient-002` from `id`, `dev-patient-001` from `id`, and `female` from `gender`; the null gender is dropped. Both the visible metrics and the HTTP response report three candidate rows. The scenario asserts all five visible examples, both checked input choices, and the candidate row count before accepting a sample.

## Measurement contract

Before starting the clock, the runner reads and retains only the identity fields from the current BuilderState API response. The clock then starts before editor setup, so the measurement includes the user action, context and capability requests, backend resolution/proposal query, and the browser render through the exact applicable comparison. It excludes initial page load and the identity read. A sample must show the expected comparison rows, an enabled Confirm action, and a proposal response containing `proposalId`. The runner compares the proposal’s `outputId`, `draftVersion`, `draftDigest`, and `snapshotToken` with the pre-action BuilderState; it compares `baseReceiptId` with the BuilderState receipt when present. When BuilderState has no receipt, the same proposal response supplies `baseReceiptId` after its other identities match the pre-action state. It records Fetch/XHR requests to the configured API origin and same-origin proxied API paths (`/api/`, `/graphql/`, `/readyz`, `/healthz`), safe Server-Timing headers and whitelisted response metrics, request failures and cancellations, checked input labels, base and candidate row counts, visible comparison row count and width, comparison schema and row hashes, and hashed proposal identity fields. It excludes same-origin development JavaScript chunks and does not log authorization headers or response bodies.

Each workload/concurrency run starts fresh Chrome profiles. Its first successful preview is labeled `cold-client`; before later `warm-client` previews, the runner reloads the same Builder URL and waits for the initial action control before starting the timer. This resets editor state while keeping the browser profile cache warm. These labels describe browser profile history only. Backend cache state is reported only when the server returns an explicit cache metric. `--concurrency` starts that many independent Chrome profiles and reports each concurrency level separately. The optional supersession probe records canceled and failed API requests.

The runner observes the existing complete source context and does not truncate source rows before grouping, pivoting, or preview. The current workload uses the fixture’s two source rows and a five-row preview bound.

For assertion diagnosis, `--capture-assertion-dom` writes only the visible comparison subtree’s headers, changed-row identity, and rendered cells to a mode-0600 JSON artifact in the mode-0700 run directory. Setup wait failures print visible construction/UI04 test IDs and the bounded TableShape error text. The capture strips the page query string and does not capture form values, tokens, network bodies, or unrelated page content. Use this option only with the isolated synthetic fixture.

## Rerun

Run the doctor first and use the exact project, generation, and Builder URL it reports. Then run a scenario JSON against that same target:

```bash
node scripts/construction_preview_bench.mjs doctor \
  --no-auth \
  --api-url http://127.0.0.1:8186/api/v1/projects/PROJECT/explorers/EXPLORER/authoring/v2/builder \
  --page-url 'http://127.0.0.1:30006/?project=PROJECT&explorer=EXPLORER&mode=builder'

node scripts/construction_preview_bench.mjs run \
  --no-auth \
  --scenario scripts/construction_preview_bench.fixture-v1.json \
  --samples 10 \
  --concurrency 1 \
  --capture-assertion-dom \
  --artifacts .artifacts/construction-preview
```

The scenario must assert the exact rendered comparison rows and cancel each proposal without applying it. Do not treat a visible comparison or an enabled Apply button alone as a successful sample.
