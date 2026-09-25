# Construction preview performance baseline

## Status

One exact cold-client pilot sample completed at 2,692.4 ms. It matched all five visible comparison examples, the checked `id` and `gender` inputs, a base count of 2, a candidate count of 3, and an applicable proposal. It recorded 35 changed comparison cells across five examples, zero canceled requests, and zero failed requests. The report is `/private/tmp/construction-preview-artifacts/2026-09-25T00-30-31-634Z/report.json`.

This is a pilot, not a latency distribution. That run used an earlier timing filter that counted development UI chunks as API requests, so its per-route timing summary is not used. The harness now limits API measurements to Fetch/XHR requests on the configured API origin. A later fresh-page diagnostic opened TableShape settings but received a visible `internal server error` before the mode selector appeared. Root confirmed from API logs that both table-shape capabilities and interpretation preview fail strict JSON decoding with `json: unknown field "label"`. No timing from that failed run is included. The 10-sample cold/warm distribution is pending the P01 decode fix and a stable API build; no distribution p50/p95 is reported yet.

## Fixture and workload

The checked local stack used UI `http://127.0.0.1:30006`, API `http://127.0.0.1:8186`, project `loom_dev_c89a69d7e137`, Explorer `loom-dev-bootstrap`, and generation `fixture-v1`. The read-only Builder doctor passed with HTTP 200 and a ready V2 Builder state. Its report is `.artifacts/loom-dev/c89a69d7e137/report.json` in the integration worktree.

The fixture has two Patient rows and three authored text columns: `id`, `name[].family`, and `gender`. A direct, read-only table-shape capabilities request returned HTTP 200, listed Unpivot as supported, and marked derived calculation unsupported because the compiler output has no scalar numeric columns. The Calculate path was abandoned before a proposal was issued.

The browser selected Unpivot over `id` and `gender`, with null rows dropped, proposed the change, and rendered an applicable comparison. It did not click Confirm or Apply. The comparison shows the two original Patient rows removed and three new rows: `dev-patient-002` from `id`, `dev-patient-001` from `id`, and `female` from `gender`; the null gender is dropped. Both the visible metrics and the HTTP response report three candidate rows. The scenario asserts all five visible examples, both checked input choices, and the candidate row count before accepting a sample.

## Measurement contract

The runner starts the clock before editor setup, so the measurement includes the user action, context and capability requests, backend resolution/proposal query, and the browser render through the correct applicable comparison. It excludes initial page load. It records API-origin Fetch/XHR durations, safe Server-Timing headers and whitelisted response metrics, request failures and cancellations, checked input labels, base and candidate row counts, visible comparison row count and width, comparison schema and row hashes, and proposal identity hashes. The workload metadata records source row count and expected output width. It does not log authorization headers or response bodies.

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
