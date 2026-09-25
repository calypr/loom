# Construction preview performance baseline

## Status

The validated 10-sample run completed at 2026-09-25 01:27 UTC against the integrated local fixture. Its report is `/private/tmp/construction-preview-artifacts/2026-09-25T01-26-57-199Z/report.json`. The API server process stayed at PID `3745087` from before the run through the post-run check.

All 10 samples passed the exact comparison rows, candidate row count, enabled Confirm, `proposalId`, and pre-action BuilderState identity checks. Each produced 3 candidate rows from 2 base rows and 35 changed comparison cells across five examples. The run observed zero canceled and zero failed API requests at concurrency 1. The earlier 2,692.4 ms pilot and all failed identity diagnostics are excluded from these distributions.

| Client label | Samples | Median | p95 |
| --- | ---: | ---: | ---: |
| Cold client | 1 | 1,761.8 ms | 1,761.8 ms |
| Warm client | 9 | 1,434.7 ms | 1,777.7 ms |
| All samples | 10 | 1,441.6 ms | 1,777.7 ms |

The cold lane has one observation, so its p95 is that single sample. “Warm” means the same Chrome profile was reloaded between later previews; it does not establish backend cache state.

The measured API request durations show `table-shape-proposals` as the longest endpoint on the critical path. Capability and semantic-inventory requests overlap; resolution completes before the proposal call begins.

| API endpoint | Samples | Median | p95 |
| --- | ---: | ---: | ---: |
| `table-shape-proposals` | 10 | 688.9 ms | 787.0 ms |
| `table-shape-resolutions` | 10 | 300.6 ms | 505.1 ms |
| `table-shape-capabilities` | 10 | 320.4 ms | 482.3 ms |
| `semantic-inventory` | 9 | 182.7 ms | 347.4 ms |

These are full loopback request durations, not isolated server execution timings. The API did not return useful `Server-Timing` phase values in this run. Proposal latency is the strongest measured candidate for a follow-up: split its server timing into query/evaluation and comparison assembly, then test reuse of the resolved base context keyed by the exact BuilderState identity. Keep such a change only if proposal latency improves and every exact row and identity gate remains green.

The report’s request-level category summary is correctly classified. Its detailed `resourceTiming.category` labels came from the pre-fix classifier, which labeled proposal paths as context-resolution because `/explorers/` matched first. The harness classifier is corrected in the current source; endpoint-specific figures above use the `route` endpoint names from that report.

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
