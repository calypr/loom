# Chromium verification hillclimb

The target is `scripts/browser/run.mjs --feature all`: native column drag and
row identity, four exact downloads, and saved-route Cancel/Apply/reload/late
response behavior. These are component browser checks with controlled data,
not deployed Loom acceptance. Lightpanda migration is deferred.

Five alternating comparisons per configuration confirm **1.51x faster with a
full build**, and **2.15x faster on repeat runs with a validated build cache**.

| Complete process wall time | Original median (range), seconds | Optimized median (range), seconds | Speedup |
| --- | --- | --- | --- |
| Cold, candidate cache disabled | 7.290 (7.224–7.408) | 4.817 (4.772–4.989) | 1.51x |
| Repeat run, candidate verified cache hit | 7.253 (7.144–7.417) | 3.376 (3.340–3.480) | 2.15x |

The original always rebuilds. Warm-cache results describe repeat developer
runs, not clean CI installations. The optimized first default invocation still
builds and publishes the cache. These local measurements use Node 22.22.0 and
Chrome for Testing 153.0.8010.12 on macOS ARM64, ten logical CPUs and 64 GiB RAM.
Host load averaged roughly 6–7 with unrelated workloads; neither side ran
concurrently with another browser benchmark. Profiling runs are excluded.

## Kept changes

- Cancel the shutdown deadline when Chromium exits. The old losing
  `Promise.race` timer kept Node alive for about another second, after the
  runner had already recorded its own duration.
- Replace unconditional 40 ms click pauses with instant scrolling, layout
  measurement and a hit test. Poll outstanding conditions every 10 ms with
  the same five-second deadline. Native pointer input remains native.
- Wait for the drag handle to be hit-testable. The menu initially mounts
  hidden, then its production positioning effect makes it visible.
- Await Chromium's download-completed event before reading each attachment.
  File existence alone can observe an empty, unfinished attachment.
- Require a new top-level document loader for every navigation/reload and
  count all ten required documents. Old DOM cannot satisfy a reload check.
- Cache built fixture JS/CSS under `.cache/browser-components`. The key
  includes source, package/lock/compiler inputs, Node and build-tool versions,
  and checkout location. Restore verifies every asset hash before copying it
  to the run's private directory. Concurrent publishers stage privately and
  publish atomically. Invalid caches trigger a build; `--no-cache` forces one.

No production UI code, debounce duration, native drag pacing, reload count,
attachment checks, stale-response observation window or screenshot was removed.

## Rejected experiments

- Minified JS plus immutable HTTP caching: three alternating comparisons gave
  7.154 s versus 7.107 s process medians, a 0.7% difference inside host variation.
  Reverted rather than claiming a win.
- Removing movement sleeps and using presence-only drag coordinates: failed
  the native drag assertion. Original drag pacing remains; geometry readiness
  fixes the hidden-menu race instead.

## Reproduce

Use Node 22 and the same Chromium executable on both checkouts. Install the UI
dependencies with `npm ci --prefix ui --ignore-scripts --no-audit --no-fund`.
The original baseline is commit `c613a9e5b59349a928d0413e78e81387ef02d99c`;
create a separate checkout there and install its identical dependencies.

```sh
node scripts/browser/run.mjs --feature all --chrome "$LOOM_E2E_CHROME" --out .artifacts/chromium-run
node scripts/browser/run.mjs --feature all --no-cache --chrome "$LOOM_E2E_CHROME" --out .artifacts/chromium-cold

node scripts/browser/benchmark-chromium.mjs --baseline /path/to/baseline --chrome "$LOOM_E2E_CHROME" --cache cold --runs 5 --out .artifacts/chromium-cold-comparison
node scripts/browser/benchmark-chromium.mjs --baseline /path/to/baseline --chrome "$LOOM_E2E_CHROME" --cache warm --runs 5 --out .artifacts/chromium-warm-comparison
```

Every output directory must be new. The benchmark measures complete subprocess
wall time, including startup, fingerprinting and exit. It alternates sides,
checks stable per-side source fingerprints and Chromium versions, requires
identical native observations, four owned downloads, both screenshots and full
cleanup. Repeated dragover event counts can vary; their event types and trusted
flags must match. Warm mode primes the candidate once, excludes that run, and
requires every timed candidate to report a verified cache hit. The original
baseline rebuilds every time. Cold mode disables candidate build caching too.

## Evidence

Raw receipts, failed experiments and the diagnostic CPU profile live under
`.artifacts/chromium-hillclimb/` and are intentionally ignored by Git:

- `final-cold/comparison.json`: five original/candidate pairs, cache disabled.
- `final-warm/comparison.json`: five pairs plus one excluded candidate prime.
- Final runner source fingerprint:
  `06a85de5f9bc4a4acdd300408cc36f78ef7b73ad1520678bda2fd93a3d690066`.
- Original runner fingerprint:
  `c73f1deac9a1a9b095b40221204b4297bb53018d35ebc732f695d7b99190ebee`.

Cold-run feature medians isolate the interaction improvements:

| Feature | Original seconds | Optimized seconds |
| --- | --- | --- |
| Drag, identities and reload | 0.959 | 0.551 |
| Four downloads with exact URL/name/bytes/hash | 0.568 | 0.292 |
| Saved-route lifecycle | 2.844 | 1.972 |

Build preparation took 1.35–1.49 seconds without caching; verified cache restore
took 6.3–7.5 ms. A separate instrumented route run observed five unchanged
300 ms production debounce timers totaling 1507.1 ms. This explains most of
the remaining approximately 1.99-second route check. The route check remains
serial because each action depends on its predecessor. Further reductions
would require different scheduling or coverage decisions, not another pause
removed without evidence.

## Verification and solo review

The final source was reviewed solo under the operator's explicit instruction.
Executable controls verify the changed failure paths:

- Suppressing all `Page.reload` calls passes the original route runner but
  fails the optimized runner at `new document committed`.
- Hiding the drag handle fails at `drag handle`; hidden geometry is rejected.
- Delaying attachment response bodies by 150 ms still produces four exact
  downloads. Both runners passed this particular diagnostic; it is not counted
  as a demonstrated original failure.
- Corrupting cached `app.js` marks the cache invalid, rebuilds and passes the
  real download checks. A source edit produces a different key and a miss.
- Changing fixture source during compilation fails before cache publication.
- Concurrent cache publishers, wrong keys, corrupt bytes and unsafe manifest
  paths are checked in the cache regression test.

Every browser control released Chromium, server and temporary profile. The
focused Node selection passes 38 tests. Inventory finds all 135 Node test
files with zero exclusions; inventory is discovery, not a full-suite pass.
The canonical `verify-builder-formal-core.mjs --phase browser --feature all`
entry point passes on the final fingerprint after all control mutations were
restored. Both screenshots were generated; the drag screenshot was inspected.
No production UI code changed, so backend and formal-model suites were not
rerun. These measurements do not establish deployed Loom performance.
