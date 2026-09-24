# Construction preview performance baseline

## Status

No valid latency sample has been recorded yet. I discard a run unless the visible comparison matches the expected row exactly and the proposal is applicable. The latest attempt produced an applicable Unpivot comparison, but the expected row did not match; the run therefore supplies no timing result. No median or p95 values are reported.

## Fixture and workload

The checked local stack used UI `http://127.0.0.1:30006`, API `http://127.0.0.1:8186`, project `loom_dev_c89a69d7e137`, Explorer `loom-dev-bootstrap`, and generation `fixture-v1`. The read-only Builder doctor passed with HTTP 200 and a ready V2 Builder state. Its report is `.artifacts/loom-dev/c89a69d7e137/report.json` in the integration worktree.

The fixture has two Patient rows and three authored text columns: `id`, `name[].family`, and `gender`. A direct, read-only table-shape capabilities request returned HTTP 200, listed Unpivot as supported, and marked derived calculation unsupported because the compiler output has no scalar numeric columns. The Calculate path was abandoned before a proposal was issued.

The browser then selected Unpivot over `id` and `gender`, with null rows dropped, proposed the change, and rendered a comparison with an enabled Confirm button. It did not click Confirm or Apply. The exact first changed-row assertion failed, and the headless Chrome profile closed with the proposal unapplied. The old harness did not retain a DOM snapshot, so the rendered row could not be inspected after close. The runner now has opt-in sanitized comparison DOM capture on assertion failure; the next run will use it to establish the exact expected row before collecting a distribution.

## Measurement contract

The runner starts the clock before editor setup, so the measurement includes the user action, context and capability requests, backend resolution/proposal query, and the browser render through the first correct applicable comparison. It excludes initial page load. It records route-level browser request durations, safe Server-Timing headers and whitelisted response metrics, request failures and cancellations, output row count, output width, row-schema hash, visible-row hash, and proposal identity hashes. It does not log authorization headers or response bodies.

`--samples` repeats the scenario in a fresh Chrome profile. The first successful preview is labeled `cold-client`; later previews in that profile are `warm-client`. These labels describe browser profile history only. Backend cache state is reported only when the server returns an explicit cache metric. `--concurrency` starts that many independent Chrome profiles and reports each concurrency level separately. The optional supersession probe records canceled and failed requests.

The runner observes the existing complete source context and does not truncate source rows before grouping, pivoting, or preview. The current workload uses the fixture’s two source rows and a five-row preview bound.

For assertion diagnosis, `--capture-assertion-dom` writes only the visible comparison subtree’s headers, changed-row identity, and rendered cells to a mode-0600 JSON artifact in the mode-0700 run directory. It strips the page query string and does not capture form values, tokens, network bodies, or unrelated page content. Use this option only with the isolated synthetic fixture.

## Rerun

Run the doctor first and use the exact project, generation, and Builder URL it reports. Then run a scenario JSON against that same target:

```bash
node scripts/construction_preview_bench.mjs doctor \
  --no-auth \
  --api-url http://127.0.0.1:8186/api/v1/projects/PROJECT/explorers/EXPLORER/authoring/v2/builder \
  --page-url 'http://127.0.0.1:30006/?project=PROJECT&explorer=EXPLORER&mode=builder'

node scripts/construction_preview_bench.mjs run \
  --no-auth \
  --scenario PATH_TO_SCENARIO.json \
  --samples 10 \
  --concurrency 1 \
  --capture-assertion-dom \
  --artifacts .artifacts/construction-preview
```

The scenario must assert the exact rendered row and cancel each proposal without applying it. Do not treat a visible comparison or an enabled Apply button alone as a successful sample.
