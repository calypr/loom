# Run the browser engine comparison

Run from the repository root with Node 22.18 or newer. The runner builds the
existing production-component fixture once and executes the same Playwright
actions and assertions on either browser. It does not start Docker or contact a
deployed Loom service.

Install the pinned client and its normal optimized test browser:

```sh
npm ci --prefix scripts --ignore-scripts --no-audit --no-fund
npm ci --prefix ui --ignore-scripts --no-audit --no-fund
node scripts/node_modules/playwright/cli.js install chromium --only-shell
lightpanda version
```

Install Lightpanda separately from its [official distribution](https://github.com/lightpanda-io/browser).
The recorded experiment uses `1.1.0-nightly.11745+1b60dcc7a`.
The report records the installed version; a newer build requires fresh evidence.
`LIGHTPANDA_BIN` or `--lightpanda` selects another executable.

Run all four candidate checks on each engine:

```sh
node scripts/browser/compare-engines.mjs --runs 1 --out .artifacts/lightpanda/compatibility
```

The checked-in candidates cover exact PreviewTable rows across reload, native
row-identity inspection, the unchanged retained disclosure helper, and saved
RelatedExpand Cancel/Apply/restore/reload with obsolete-response rejection.
Each check uses a fresh browser context. The route and table fixtures use actual
production components with controlled data, services and sessionStorage callers.
They do not establish public API persistence or complete Builder acceptance.

For five alternating trials per engine and the strict speed gate:

```sh
node scripts/browser/compare-engines.mjs --out .artifacts/lightpanda/comparison --require-speedup
```

`--case preview-load`, `--case preview-identity`, `--case disclosure` or
`--case saved-route` selects a focused check. A subset cannot qualify the whole
suite. `--engine chromium` or `--engine lightpanda` executes only that engine.
`--chrome` selects a system browser. Use the pinned headless shell for the
adoption decision; a system Chrome startup comparison can mislead.

Reports distinguish browser startup, context/journey/cleanup, complete trial,
and shared build preparation. Both prepared and build-inclusive median ratios
must exceed 5. The cold trial starts a browser once for the selected suite;
per-case timings show whether startup savings survive browser reuse.
Failed or different observations never produce a speed ratio. A timing
candidate still requires limiter evidence and a representative deployed journey.

Exit 0 means every selected test passed, even if the browser was too slow.
Exit 1 means a test, setup, source-identity or cleanup failure.
With `--require-speedup`, exit 2 means the timing/adoption prerequisite did not
qualify. Read `comparison.verdict` and `comparison.adopted` in `comparison.json`.
The runner never automatically enables a new CI suite.

Use an empty output directory for each invocation. Per-trial JSON, browser
errors, failed requests, HTTP statuses, exact observations, asset hashes and
source fingerprints stay under that directory. The runner checks its source
fingerprint again after execution and stops its own processes and loopback
server. Generated evidence is ignored by Git.

For diagnosis, add `--trace --runs 1` to save Playwright trace ZIPs. Trace samples
are explicitly ineligible for the speed decision. To check that wrong visible
rows fail the literal oracle:

```sh
node scripts/browser/compare-engines.mjs --engine chromium --case preview-load --runs 1 --fault preview-row --out .artifacts/lightpanda/wrong-row
```

This command must exit 1. The fault changes one test-owned fixture row before
bundling; it does not modify a working-tree file or demonstrate a production
mutation. Fault runs cannot produce performance evidence.

Regenerate the file-level historical inventory:

```sh
node scripts/audit-lightpanda-coverage.mjs > .artifacts/lightpanda/inventory.json
node --test scripts/browser/engine-verdict.test.mjs
```

The audit requires the recovery commit `5646633e` in local Git history. It reads
all 96 retired specs, retains exact source hashes, literal test titles, imports,
Git recovery links and a disposition for each file. Generated titles and nested
workflow assertions are not represented as separate tests. The family grouping
is a consolidation guide, not proof that those assertions are duplicates.

Read [the coverage decisions](COVERAGE.md) and [the measured outcome](RESULTS.md)
before proposing adoption.
