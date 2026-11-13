# Backend benchmark harness (Arango/AQL vs PostgreSQL/SQL)

Matched dataframe-task benchmark. Correctness gates run before any timing
is claimed; every derived number in an artifact is recomputed by the Go
verifier, so a hand-edited report never verifies.

## Layout

- `fixtures.go` — deterministic NDJSON dataset (preset × seed → digest).
- `authoring.go` — battery workloads in acceptance-proven workspace shapes.
- `canonical.go`, `oracle.go`, `gates.go` — bag canonicalization, expected
  results, correctness gates (bag equality, shape, auth, truncation).
- `measure.go`, `controls.go` — sampling, summaries, stability CV,
  self-ratio, empty-result floor.
- `matrix.go`, `armplan.go` — battery matrix; run scopes and leg plans.
- `report.go`, `report_md.go` — artifact schema, `VerifyReport`, Markdown.
- `harness.go`, `provision.go`, `driver.go` — live arms, MockFence, server
  spawn, RunPlan-checked driver.
- `cmd/benchcli` — CLI below. `scripts/benchmark-backends.mjs` — node
  orchestrator (evidence lines only; Go owns verification authority).

## Scopes

| scope          | arango leg      | postgres leg    | use                                      |
| -------------- | --------------- | --------------- | ---------------------------------------- |
| `both-arms`    | run             | run             | CI target (needs both services)          |
| `postgres-only`| not_run + capability | run        | worker sessions with a granted window    |
| `planned`      | not_run + capability | not_run + capability | pre-window disclosure artifact     |

A not_run leg must name its missing capability; the artifact carries it per
workload, and `VerifyReport` refuses samples, summaries, claims, versions,
or control values for legs that did not run. A planned report can never be
`passed`: it is `failed` with zero claims, and the node orchestrator
refuses a planned artifact that captured any timing.

## Commands

```sh
go run ./internal/dataframe/benchmark/cmd/benchcli verify --report OUT/report.json
go run ./internal/dataframe/benchmark/cmd/benchcli run --scope planned \
  --source-commit <40-hex> --out OUT
go run ./internal/dataframe/benchmark/cmd/benchcli run --scope postgres-only \
  --source-commit <40-hex> --window-note "granter + time" --postgres-dsn <dsn> --binary <loom-server> --out OUT
# database lifecycle (admin DSN via LOOM_BENCH_ADMIN_DSN env, never argv):
... setup-pg --database loom_bench_x   # six-series ladder via SetupMigrate
... drop-pg --database loom_bench_x
```

Artifacts per run: `report.json` (verified schema), `report.md`,
`arms.json` (operational evidence: arm URLs, log paths, fence requests or
planned capabilities).

## Exit codes (`run`)

 - `0` — the live legs ran and the report status is `passed`.
 - `1` — the run measured something and failed (a correctness gate failed,
   or any leg failed after real timings were captured).
 - `3` — disclosed not_run, never a pass: preflight refused (missing
   `LOOM_BENCH_LIVE=1`, window note, source commit, namespace guard,
   existing output), the planned disclosure artifact was written, or a
   live run ended non-passed with zero measured samples (for example the
   default `--publication=excluded` path, where every gate arm is
   blocked). A run that timed nothing must not look like a success.

## Publication chain (ClickHouse-backed) and the timed lane

The publication *chain* — dataset → bundle →
`publication.Target`/`ClickHouseBundleStore`, the chain's only production
implementation — is ClickHouse-backed. The timing battery is not: the
authoring battery (explorer publish, builder, reconcile, preview,
receipts, workload gates, timing, controls) runs for real against both
drivers with `server.clickhouse.enabled=false`, the same posture the
`backend-conformance` CI lane adjudicates on the actual services. The
PostgreSQL backend forbids `clickhouse.enabled` in this slice outright,
so the chain and the battery must be disclosed separately.

- `--publication=excluded` (default): spawn, upload, and version probes run
  for real on every ran leg; the whole publication chain ships as
  planned/not_run under the capability `CH-<pin>-unavailable-in-container`
  (`PublicationCapabilityCH`, derived from `ClickHouseCIVersion`, which
  must equal the `clickhouse/clickhouse-server` tag in
  `.github/workflows/tests.yaml` — currently 25.3). `VerifyReport` then
  refuses any samples, summaries, claims, or control values, requires every
  gate arm blocked, and one disclosure row per battery workload per ran leg.
  This mode measures nothing and can never satisfy a benchmark check.
- `--publication=timed`: the paired timing lane. The battery executes for
  real on every ran leg with `clickhouse.enabled=false`; only the chain
  ships disclosed not_run under `PublicationCapabilityTimed`
  (`CH-not-deployed-for-timed-dataframe-legs`). Reports from this lane
  carry real samples, summaries, controls, and claims; `VerifyReport`
  still refuses them if the battery flag is flipped off while evidence
  is present.
- `--publication=run`: requires `--clickhouse-enabled`, i.e. the CI lane
  with the pinned service. A report whose identity says ClickHouse is
  disabled while claiming the chain ran fails verification.

## Paired speed comparison (operator acceptance, 2026-11-13)

`node scripts/benchmark-backends.mjs --paired --binary <server> --benchcli
<benchcli> --out <dir> [--profile tiny|small|representative]
[--iterations N>=5 --warmup N>=1 --rounds N] [--source-commit <40-hex>]`
runs the same matched battery against BOTH real drivers discovered from
`LOOM_TEST_ARANGO_URL` + `LOOM_TEST_POSTGRES_URL` (the same variables the
`backend-conformance` job exports; the CI job
`factory/paired-driver-benchmark` in `.github/workflows/tests.yaml` runs
this exact command with services `arangodb:3.12` + `postgres:18`). With
`LOOM_BENCH_ADMIN_DSN` set it first provisions its own fresh
`loom_bench_*` database via `benchcli drop-pg`/`setup-pg` (database,
six-series schema ladder, migrated tables) — the PostgreSQL backend
readiness answers `schema_missing ... the caller owns schema lifecycle`,
so the lane, not the server, bootstraps. A failed `setup-pg` fails the
check before any timing attempt.
Correctness gates decide first; then >= 5 alternating measured timings per
driver are required. The check FAILS — never skips, never passes — when a
service is missing, an arm is planned/not_run/excluded (beyond the two
reviewer-accepted live-battery exclusions), a driver produced no usable
timing samples, or the Go verifier rejects the artifact. On success it
writes `comparison.md` (median/min/max/range in milliseconds per workload
per driver, plus versions, source SHA, fixture digest, warmup/cache/
rounds/concurrency conditions) beside the raw `report.json`. No speedup is
claimed unless the claim clears correctness and the stability control;
either driver may win.

## Live-run guards (all fail closed, exit 3)

`LOOM_BENCH_LIVE=1` (coordinator-granted exclusive window), `--window-note`,
full 40-hex `--source-commit`, no overwrite of `--out/report.json`,
PostgreSQL database/schema and Arango database under `loom_bench_*`, arms
uniformly attached or spawned (never mixed), spawned arms get tokens only
from the MockFence.

## Current blockers (cited in planned artifacts)

- ClickHouse service: needed only by the publication *chain*
  (`--publication=run`), which requires `clickhouse/clickhouse-server:25.3`
  as provisioned by the CI lane. Paired timing uses
  `--publication=timed` and does not wait on it. Owner: CI lane.
- `PG-PG-AUTH-PATH-DISCOVERY-ALLGENS` — FIXED in PR #156 (verified live:
  upload accepted, fence hit once, no 403); the branch base predates the
  library-side fix, so live legs go green when it merges. Evidence:
  `pg-pg-auth-path-discovery-allgens-evidence.md` (session artifacts).
- `PG-PG-CAPABILITY-EVIDENCE-INVENTORY-ONLY-STORE` — FIXED in PR #158
  (`NewFullStore` composition); verified live: no panic, reaches
  materialization. Evidence: `pg-pg-capability-evidence-inventory-only-store-evidence.md`.
- Arango leg: no ArangoDB service in the worker container; go-arango CI
  lane targets it.
