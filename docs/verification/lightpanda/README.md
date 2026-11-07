# Run pooled browser checks

Run from the repository root with Node 22.18 or newer:

```sh
npm ci --prefix scripts --ignore-scripts --no-audit --no-fund
npm ci --prefix ui --ignore-scripts --no-audit --no-fund
node scripts/node_modules/playwright/cli.js install chromium --only-shell
lightpanda version
```

Install Lightpanda from its [official distribution](https://github.com/lightpanda-io/browser).
The measured release is `1.1.0-nightly.11745+1b60dcc7a`. Playwright is pinned to
`1.63.0`. `LIGHTPANDA_BIN` or `--lightpanda` selects another executable.
`--chrome` selects another Chromium executable. New versions need fresh evidence.

## Execute the reusable Lightpanda pool

```sh
node scripts/browser/compare-engines.mjs --engine lightpanda --concurrency 8 --jobs 64 --runs 1 --out .artifacts/lightpanda/run
```

The default mode is `throughput`. Each Lightpanda worker owns a process, browser
context, page and private HTTP cache. Chromium uses one browser with isolated
worker contexts/pages and its native cache. Workers retain these resources across
jobs, clear caller storage between jobs, and close at the end. Single-engine
commands retain their pool across all batches. Comparative trials close the
active pool before starting the competitor so idle browser memory does not
distort the comparison. Independent Lightpanda processes can execute V8 work on separate cores.

Two named DOM contracts execute real production components: `preview-content`
checks literal PreviewTable rows/labels across reload, and `viewer-link` checks
both configured FileCell URLs across reload. Browser-side DOM readiness and a
single DOM snapshot avoid locator actionability and extra protocol calls for
these DOM-only guarantees. They do not prove native visibility, pointer input,
drag, download bytes, public API persistence or full Builder acceptance.

A 64-job batch repeats those two contracts 32 times each. It is not 64 distinct
restored tests. Every document has a job identity, every observation must match
it, and the no-store HTML request count must match every navigation and reload.
This rejects stale observations and skipped work. Both engines use the same
minified production bundle and immutable assets served from memory.

Lightpanda skips external stylesheets in the DOM-only lane, as allowed by that
contract. This resource policy is explicit in the report. Native compatibility
enables stylesheet loading so computed visibility and pointer styles participate.

## Tune and compare sustained throughput

```sh
node scripts/browser/compare-engines.mjs --jobs 128 --concurrency 1,4,8,16,32 --out .artifacts/lightpanda/comparison --require-speedup
```

The runner explores the concurrency list for each engine. Only correct, fully
cleaned tuning batches qualify. It selects the lowest concurrency within 5% of
the best observed steady work time, then confirms the choice with five separate
batches per engine, reversing engine order on alternating iterations. Tuning
times do not enter the confirmation result. Only one engine is resident during
each independent comparative trial. Its workers persist across every job in
that batch; startup/warmup/shutdown are counted for that trial. Single-engine
execution retains its pool across the entire requested stream of batches.

The default sweep is 1, 2, 4 and 8, capped to leave two logical CPUs of headroom.
Explicit values up to 32 explore additional I/O concurrency. More browser slots
do not create CPU cores. Size the sweep for the machine's shared workload and
memory. The comparative mode never retains an idle competitor pool.

`steadyComparison` measures assertion-checked work after warmup. `comparison`
includes pool setup/shutdown and also reports build-inclusive timing. Shared
single-engine pools amortize their lifecycle across batches; independent
comparative trials include their own lifecycle. `poolLifecycle` records the actual setup,
warmup, shutdown, pool count and complete suite costs. Per-job durations, jobs/second,
driver CPU, server request counts, asset hashes and errors remain in the report.
Both complete and build-inclusive speed ratios must exceed 5. A timing candidate
is scoped to these DOM contracts and cannot establish whole-application adoption.

## Preserve native browser compatibility

```sh
node scripts/browser/compare-engines.mjs --mode compatibility --runs 1 --out .artifacts/lightpanda/native
```

This runs the original four candidates with fresh contexts and unchanged native
assertions. Lightpanda gets a private HTTP cache and `--load-resources stylesheet`.
No DOM event injection replaces an input action. Use `--case` to select a
contract in the current mode. Existing native Chromium drag/download/route
acceptance remains in `scripts/browser/run.mjs`.

## Diagnose and verify failures

Each invocation needs an empty output directory. Exit 0 means the selected jobs
passed, even if too slow. Exit 1 means a check, setup, source or cleanup failure.
With `--require-speedup`, exit 2 means the speed prerequisite did not qualify.
The runner never automatically enables a new acceptance suite.

Use `--engine lightpanda --profile --runs 1 --jobs 128 --concurrency 8` for owned
process CPU/RSS samples. Use `--trace --runs 1 --jobs 2 --concurrency 1` for
Playwright traces. Diagnostic samples cannot qualify as performance evidence.

These negative controls must both exit 1 on either engine:

```sh
node scripts/browser/compare-engines.mjs --engine lightpanda --jobs 8 --concurrency 4 --runs 1 --fault preview-row --out .artifacts/lightpanda/wrong-row
node scripts/browser/compare-engines.mjs --engine lightpanda --jobs 8 --concurrency 4 --runs 1 --fault job-identity --out .artifacts/lightpanda/wrong-job
```

The first changes one test-owned row during bundling. The second returns a wrong
document identity for one job. Neither modifies production source or supplies
performance evidence.

```sh
node scripts/audit-lightpanda-coverage.mjs > .artifacts/lightpanda/inventory.json
node --test scripts/browser/batch-scheduler.test.mjs scripts/browser/engine-verdict.test.mjs
```

The audit reads all 96 retired specs at recovery commit `5646633e`, which must
exist locally. It records source hashes, literal titles, imports and file-level
dispositions. It does not prove duplicate assertions or close historical gaps.
See [coverage](COVERAGE.md), [pooled results](POOLED_RESULTS.md) and the
[initial serial experiment](RESULTS.md). Generated evidence stays outside Git.
