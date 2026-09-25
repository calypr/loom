# Construction preview performance baseline

## Status

One exact cold-client pilot sample completed at 2,692.4 ms. It matched all five visible comparison examples, the checked `id` and `gender` inputs, a base count of 2, a candidate count of 3, and an applicable proposal. It recorded 35 changed comparison cells across five examples, zero canceled requests, and zero failed requests. The report is `/private/tmp/construction-preview-artifacts/2026-09-25T00-30-31-634Z/report.json`.

This is a pilot, not a latency distribution. That run used an earlier timing filter that counted development UI chunks as API requests, so its per-route timing summary is not used. A later diagnostic found and helped fix two instrumentation gaps: the demo sends API requests through Vite’s same-origin `/api/` proxy, and response-body capture had keyed off a coarse request category rather than the endpoint path. The current harness captures same-origin API paths, extracts safe proposal identity fields, and compares output, base receipt, draft version, and draft digest against the visible Builder state. The API decoder issue that returned `json: unknown field "label"` has since been fixed. A post-fix sample reached the exact visible comparison, but its timing was discarded because the old filter omitted the proposal response identity. The corrected identity check still needs one successful sample. The 10-sample cold/warm distribution awaits that check and a stable API build; no distribution p50/p95 is reported yet.

## Fixture and workload

The checked local stack used UI `http://127.0.0.1:30006`, API `http://127.0.0.1:8186`, project `loom_dev_c89a69d7e137`, Explorer `loom-dev-bootstrap`, and generation `fixture-v1`. The read-only Builder doctor passed with HTTP 200 and a ready V2 Builder state. Its report is `.artifacts/loom-dev/c89a69d7e137/report.json` in the integration worktree.

The fixture has two Patient rows and three authored text columns: `id`, `name[].family`, and `gender`. A direct, read-only table-shape capabilities request returned HTTP 200, listed Unpivot as supported, and marked derived calculation unsupported because the compiler output has no scalar numeric columns. The Calculate path was abandoned before a proposal was issued.

The browser selected Unpivot over `id` and `gender`, with null rows dropped, proposed the change, and rendered an applicable comparison. It did not click Confirm or Apply. The comparison shows the two original Patient rows removed and three new rows: `dev-patient-002` from `id`, `dev-patient-001` from `id`, and `female` from `gender`; the null gender is dropped. Both the visible metrics and the HTTP response report three candidate rows. The scenario asserts all five visible examples, both checked input choices, and the candidate row count before accepting a sample.

## Measurement contract

The runner starts the clock before editor setup, so the measurement includes the user action, context and capability requests, backend resolution/proposal query, and the browser render through the correct applicable comparison. It excludes initial page load. It records Fetch/XHR requests to the configured API origin and same-origin proxied API paths (`/api/`, `/graphql/`, `/readyz`, `/healthz`), safe Server-Timing headers and whitelisted response metrics, request failures and cancellations, checked input labels, base and candidate row counts, visible comparison row count and width, comparison schema and row hashes, and hashed proposal identity fields. It compares the proposal’s `outputId`, `baseReceiptId`, `draftVersion`, and `draftDigest` with the visible Builder’s output, receipt, version, and digest. This excludes same-origin development JavaScript chunks. The workload metadata records source row count and expected output width. It does not log authorization headers or response bodies.

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
