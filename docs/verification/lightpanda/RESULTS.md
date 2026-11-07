# Lightpanda did not qualify for restoration

Do not adopt Lightpanda for Loom acceptance or restore the retired suite under
the speed premise tested here. Four useful candidate checks now execute through
the same Playwright runner on either engine, but two fail on Lightpanda. The
compatible load/reload check has no measurable journey speed advantage across
five alternating trials. The requested over-5x gate is not met.

The branch starts at PR #32's tip, `899f3bb6`. The experiment preserves existing
Chromium acceptance and adds opt-in candidates and a reproducible comparison.
It does not claim a completed migration of the old deployed journeys. Their
unclosed obligations remain in [the coverage review](COVERAGE.md).

## Measured workload and configuration

- macOS arm64, Node `v22.22.0`, ten available logical CPUs.
- Playwright `1.63.0`, locked with npm integrity hashes.
- Normal Playwright headless shell `153.0.8010.12`, build `1243`.
- Lightpanda `1.1.0-nightly.11745+1b60dcc7a`. Its CDP browser version reports
  `124.0.6367.29`; that is not the Lightpanda release identifier.
- The existing production-component fixture, production React mode, actual
  production CSS, identical 4,499,430-byte JS and 292,665-byte CSS assets.
  Bundling follows the retained fixture's normal unminified test configuration.
- Browser startup once per trial, fresh context per case, native actions,
  literal assertions, and owned browser/server cleanup. No retries.
- Timing runs are serial, with pair order reversed on alternating iterations.
  The host has other activity, with load averages around 7.5 to 8.6 during the
  final speed run. The foreground does no other verification during that run.

Initial system Chrome results included expensive browser startup. They were
discarded for adoption, and the pinned optimized headless shell was installed
for the final comparison. This matters more than the nominal browser brand.

## Equivalent successful load/reload trials

`preview-load` mounts actual PreviewTable, checks exact headers and the four
literal cell values, reloads, and repeats the observations. Every trial on both
engines passed, with no page exceptions, failed requests or HTTP errors, and
identical observations. All ten browsers and the fixture server stopped.

| Trial | Chromium journey, ms | Lightpanda journey, ms | Chromium complete, ms | Lightpanda complete, ms |
| --- | ---: | ---: | ---: | ---: |
| 1 | 333.95 | 320.42 | 541.34 | 685.21 |
| 2 | 318.94 | 309.86 | 461.87 | 632.76 |
| 3 | 287.67 | 314.63 | 460.23 | 651.37 |
| 4 | 328.69 | 313.80 | 524.82 | 623.79 |
| 5 | 306.44 | 319.84 | 516.88 | 676.87 |

The journey medians are **318.94 ms versus 314.63 ms**, a ratio of **1.01x**.
Ranges are 287.67 to 333.95 ms and 309.86 to 320.42 ms. The small median gap is
below the observed variation, so there is no measurable journey advantage.

Complete trial medians are 516.88 ms versus 651.37 ms, a ratio of 0.79x.
Ranges are 460.23 to 541.34 ms and 623.79 to 685.21 ms. Shared build preparation
takes 1,623.64 ms; including it gives 0.94x. The automated `--require-speedup`
command exits **2**, with `verdict: REJECTED` and `adopted: false`.

Separate diagnostic traces locate most journey time in navigation and reload
through the actual JS bundle. Selector reads are small. The Lightpanda trace's
navigation and reload take about 155 and 126 ms, versus about 138 and 66 ms in
Chromium. These are traced diagnostic observations, excluded from the five-run
performance result. Cold Lightpanda trials also pay CDP disconnect/process
shutdown costs. Removing rendering work alone does not remove navigation,
module execution, assertions, context setup or teardown.

This is a local component-test result, not a general Lightpanda regression or a
measurement of the full CDA application. No deployed application was changed,
started, or timed. Full-CDA query latency is not included. A larger deployment
could behave differently, but these runs supply no evidence for over-5x gains
and cannot justify restoring its slow suites.

## Native interaction compatibility

| Candidate | Pinned Chromium | Lightpanda |
| --- | --- | --- |
| Exact PreviewTable load/reload | Passed | Passed |
| Native row-identity inspection | Passed | Failed: cell intercepts the identity button's pointer event. |
| Unchanged retained disclosure helper | Passed | Passed, including hidden-fill rejection and native typing. |
| Saved route Cancel/Apply/restore/reload | Passed | Failed: route B never mounts after opening the other-routes disclosure. |

The standalone disclosure pass does not establish React disclosure equivalence.
The direct diagnostic probe observes `details.open === true`, while the
production component's `onToggle`-driven state does not expose its conditional
route controls. This is an observed compatibility gap; this experiment does
not establish Lightpanda's internal root cause.

Failed journeys retain their errors and return exit 1. Their timeouts never
become speed ratios. There is no forced click, DOM event injection, hidden
assertion removal, or fetch substitute for a download.

## Verification and evidence

The literal-row negative control renders `wrong-patient` in the real browser
and fails the unchanged expected-row assertion on both engines, with exit 1.
This is an assertion self-check using wrong test-owned data, not a production
source mutation. Five Node gate tests additionally reject fast failures, wrong
observations, missing cases, leaks, bad timings, inadequate samples and noise.

The focused Node checks pass **93 assertions** across the new verdict tests,
verification inventory and development CLI tests. The new verdict suite is
included automatically by the existing Node CI inventory.

The retained native Chromium `--feature all` acceptance run also passes actual
trusted drag/drop, exact attachment downloads before/after reload, and the saved
route lifecycle. It reports no browser errors and confirms browser/server/profile
cleanup. Its raw evidence is `retained-chromium/browser.json`.

A broader local run selects all 133 native test files with zero exclusions. It
reports 722 passes, five failures and two cancelled files out of 729 assertions.
Two projection assertions require Go 1.26.5, while this Mac supplies Go 1.22.10
with `GOTOOLCHAIN=local`. Three row96 lifecycle assertions cannot find their
Linux-default Chrome path. The process-pool and kill-tree files reach their
30-second bounds in this restricted Mac environment. Their source is unchanged
from `899f3bb6`; this run is not a green full-suite claim. No matching task-owned
process markers remained after the run. Linux CI remains the broader gate.

Final compatibility and five-run speed evidence share source fingerprint:

```text
6e4a91780e0177a75fbf09753cc2e9cbf63d10fb840ff705e3a1e050a79a0919
```

The unmodified production asset hashes are:

```text
app.js  dc818d530822acc9c11c90dd2ec500b8689a6e6c5f4eca7c0d8323b9d6035381
app.css 8a384386447ca6375a926b94141730ef5a6c8f808f790ebd32b23e74218f6679
```

Raw evidence is machine-local under `.artifacts/lightpanda/`, outside Git:

| Path | Contents |
| --- | --- |
| `final-compatibility/comparison.json` | Final four-case results, raw errors, observations and cleanup. |
| `final-preview-five/comparison.json` | Five alternating successful trials, all timings and the rejected speed gate. |
| `diagnostic-load/` | Separate trace ZIPs; not performance samples. |
| `wrong-row-control/` and `wrong-row-lightpanda/` | Expected assertion failures on wrong visible rows. |
| `inventory.json` | All 96 historical source hashes, titles, imports and file dispositions. |
| `node-regressions.log` | Full local Node run with the environment failures above. |

SHA-256 of the final compatibility report is
`9b40956500c26e8641ed809d33b3cc45fb5e94eda925d1625b7d8e93bb0563c6`.
SHA-256 of the final five-run report is
`babe8794bdd1acfe0833e8a692aa6aba504b1604fbdfc45c1f8fca2ad7bafdc3`.
Use [the execution instructions](README.md) to regenerate evidence. New runs
have new ports, times and report hashes; compare source and asset identities.

Review was solo as explicitly requested. No independent reviewer or formal
model update is claimed. The [decision trail](decisions.tsv) records the
baseline correction, qualification rejection and the checks actually run.
