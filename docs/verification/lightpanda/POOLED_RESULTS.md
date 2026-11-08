# Persistent worker-pool results

The runner now uses Lightpanda's parallel execution and HTTP caching instead of
treating it as a cold, serial Chromium replacement. Workers retain processes, contexts, pages and caches across jobs. Single-engine
runs retain the pool across all batches. Comparative trials keep only one
engine resident at a time so idle competitor memory cannot hide an advantage. The initial
serial experiment is not the basis for this result.

On an identical cached workload, eight-worker Lightpanda execution is **3.12x
faster than one-worker Lightpanda**, verified with five alternating trials per
configuration. That is a parallelization gain within Lightpanda, not a claimed
3.12x advantage over Chromium.

No observed run qualifies for the requested over-5x Chromium-relative gate.
The final host is heavily contended, so an engine ranking is **INCONCLUSIVE**.
The measured scaling gain within Lightpanda is distinct from a claim about
Chromium, Linux/server deployment or the full CDA application.

## Configuration corrections

- Lightpanda HTTP caching is **disabled by default**. Each worker now gets an
  isolated `--http-cache-dir`; immutable assets stay cached across jobs.
- Lightpanda skips external stylesheets by default. Native compatibility now
  sets `--load-resources stylesheet`. This fixes the original row-identity click
  failure: Lightpanda now passes three of four native checks.
- Independent Lightpanda processes provide CPU parallelism. A bounded scheduler
  owns one job per worker and preserves exact job/result identity.
- The DOM lane uses a minified production bundle, cached assets served from
  memory, commit-based navigation and browser-side DOM readiness/snapshots.
  It has no fixed sleep or negative-action timeout in its measured workload.
- Single-engine pools start once for their requested stream. Comparative trials
  retain a pool across all 128 jobs, close it, then run the competitor. Cold and
  steady time are separate, and the common driver/build preparation is counted
  once per suite. No idle competitor occupies memory during comparison.

## Workload and measurements

The workload alternates `preview-content` and `viewer-link`: literal production
PreviewTable content across reload, and exact production FileCell URLs for two
configurations across reload. Each 128-job batch repeats these two contracts 64
times. This is a throughput workload, not 128 distinct restored coverage cases.

All ten confirmation batches pass: **640 jobs per engine**, zero failed jobs,
matching observations, and complete cleanup. Every batch causes exactly 384
no-store HTML requests. Both engines make **zero JS/CSS server requests during
timed work**, proving that worker caches are active while real document reloads
and JavaScript/component execution still occur.

The exploratory sweep tests 1, 4, 8, 16 and 32 workers. Configuration selection
uses sustained work time and prefers the smallest worker count within 5% of the
best exploratory result. Five separate alternating batches then confirm the
chosen settings. Tuning samples never enter the confirmation statistics.

| Workers | Chromium steady jobs/s | Lightpanda steady jobs/s |
| ---: | ---: | ---: |
| 1 | 20.21 | 9.65 |
| 4 | 58.57 | 31.64 |
| 8 | 59.49 | 37.10 |
| 16 | 58.09 | 37.86 |
| 32 | 59.88 | 38.29 |

Those are single exploratory samples, not five-run performance claims. They
identify the 4/8-worker neighborhood. The final source repeats tuning at four and eight workers and selects four
Chromium workers and eight Lightpanda workers. Earlier severely contended runs
selected different counts, so no universally optimal configuration is claimed.

| Final one-engine-resident confirmation | Chromium, 4 workers | Lightpanda, 8 workers |
| --- | ---: | ---: |
| Median 128-job steady batch | 2,561.51 ms | 3,778.02 ms |
| Steady batch range, five samples | 2,272.28–2,811.54 ms | 3,515.94–4,063.95 ms |
| Median steady throughput | 49.97 jobs/s | 33.88 jobs/s |
| Pool startup plus warmup, five trials | 2,010.11 ms | 3,552.68 ms |
| Pool shutdown/cache cleanup, five trials | 325.88 ms | 700.68 ms |
| Complete 640-job suite | 14,952.50 ms | 23,150.31 ms |

Dividing Chromium time by Lightpanda time gives **0.646x**; a value below one
means Chromium completes sooner. Including the common build once gives 0.668x.
The over-5x gate exits **2**, despite every job passing. Steady work alone gives
0.678x. No failed or incomplete job supplies a throughput result.

## Controlled Lightpanda scaling comparison

A separate A/B run alternates one and eight Lightpanda workers five times each.
Both sides use identical source, bundle hashes, 128-job workload, assertions and
HTTP caching on both sides. Every observation matches; every trial passes all
128 jobs and verifies all 384 document requests plus complete cleanup.

| Lightpanda configuration | Median steady batch | Range, five trials |
| --- | ---: | ---: |
| One worker, HTTP cache enabled | 13,838.66 ms | 12,874.14–15,989.31 ms |
| Eight workers, HTTP caches enabled | 4,435.28 ms | 4,163.06–4,550.00 ms |

The steady speedup is 3.12x. The median difference exceeds the run-to-run
spread. Including pool setup/shutdown gives 14,271.29 ms versus 5,190.10 ms.
Including common driver/build preparation as well gives 16,326.81 ms versus
6,945.40 ms, a **2.35x** improvement in the complete prepared workload.
Those samples are distinct from the Chromium comparison, so their absolute
times differ with host activity and pool lifetime. They establish the scaling
gain without comparing different test contracts or counting cache misses only
on the serial side. Reproduce by alternating the documented single-engine
command with `--concurrency 1` and `--concurrency 8`, using `--runs 1` and fresh
output directories for each of five pairs.

Both use Playwright 1.63.0 on macOS arm64, Node 22.22.0, ten available logical
CPUs, headless Chromium 153.0.8010.12 and Lightpanda
`1.1.0-nightly.11745+1b60dcc7a`. The host is shared and has external CPU/VM activity. The final run starts at
about 5.7 load average and ends at 15.9, on ten logical CPUs, with roughly 3 GB
free memory at the start. Earlier attempts reached load averages above 20 with
little free memory and selected different settings. Engine order alternates,
but that cannot create an isolated host. No global engine ranking, Linux/server
or resource-constrained deployment result is established.

## Why additional workers stop helping

The sweep plateaus around eight workers. Separate diagnostic process samples
show active Lightpanda CPU work across its processes while the Node driver uses
less than one core. API traces place the work in navigation/reload and production
page initialization, rather than locator polling. Immutable resource cache hits
remove repeated asset transport. The remaining workload still executes the
application JavaScript and creates the production DOM on every required reload.

These observations locate the limiting phase; they do not distinguish every
V8, DOM or garbage-collection cost inside Lightpanda. No diagnostic samples enter
the qualified comparison. The implementation now removes serial browser startup, missing HTTP cache,
forced negative waits and a single V8 process as explanations for its setup.
Host contention and remaining page-initialization costs still prevent a broad
performance verdict; more workers do not automatically supply more CPU.

## Native coverage and evidence

With stylesheet loading enabled, both engines pass preview load, native row
identity inspection and the unchanged disclosure visibility/typing helper.
Lightpanda still fails the saved-route case: the other-routes disclosure opens,
but its React-controlled route-B input never mounts. No synthetic toggle or
forced click closes that gap. Native Chromium drag and actual download-byte
acceptance remain separate from the DOM throughput lane.

Raw evidence stays outside Git under `.artifacts/lightpanda/`:

- `isolated-verified/comparison.json`: final tuning, five-run confirmation,
  exact observations, server/cache counters, CPU accounting and lifecycle costs.
- `final-native/comparison.json`: the stylesheet correction
  and remaining native route failure.
- `pooled-resource-profile/`: separate diagnostic CPU/RSS samples, not speed data.
- `scaling-ab-verified/scaling.json`: five alternating one-worker/eight-worker Lightpanda
  samples, exact observation/source identity checks and the 3.12x scaling result.

The final comparative source fingerprint is
`88ed5dd3f4b2526a44c968835706c5a0bb19001fe4f86f735bee8a11eff87d4d`.
The single-engine scaling receipt uses snapshot `128b31ef`; the subsequent
change affects only comparative pool residency and report metadata. The
single-engine execution path and assets are unchanged. This is reused evidence
for that unchanged path, not an assertion that the two controller hashes match.
The final memory policy applies only to multi-engine comparisons; normal
single-engine streams keep their workers alive across all requested batches.
The 1,739,269-byte production JS SHA-256 is
`2d23b0820057ec8b8ad7c088a224f8a37ebee3c652893e2cc9a3d2d4121cfdef`.
Use [the commands](README.md) to regenerate this workload. Fresh runs have new
ports/times/report hashes; verify source, asset and observation identity.

The [coverage dispositions](COVERAGE.md) remain scoped.
97 focused Node assertions pass. Wrong-row injection fails four of eight jobs
on each engine; wrong-job injection fails one of eight on each. Faulted samples
never qualify as speed evidence. A real evidence-file permission failure exits
with EACCES, leaves no terminal pass receipt, and all six captured owned processes are directly verified stopped. All persistent workers close before final receipt persistence.

No full-application or formal-model acceptance claim follows from these two DOM contracts. Review is
solo, as requested. Broader local Node limitations from the initial experiment
remain recorded in [the earlier verification](RESULTS.md); no full-suite green
claim is made.
