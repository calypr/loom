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

- `0` — live legs ran and the artifact verifies.
- `1` — a live run executed and failed.
- `3` — disclosed not_run: preflight refused (missing `LOOM_BENCH_LIVE=1`,
  window note, source commit, namespace guard, existing output) or the
  planned disclosure artifact was written.

## Live-run guards (all fail closed, exit 3)

`LOOM_BENCH_LIVE=1` (coordinator-granted exclusive window), `--window-note`,
full 40-hex `--source-commit`, no overwrite of `--out/report.json`,
PostgreSQL database/schema and Arango database under `loom_bench_*`, arms
uniformly attached or spawned (never mixed), spawned arms get tokens only
from the MockFence.

## Current blockers (cited in planned artifacts)

- `PG-PG-AUTH-PATH-DISCOVERY-ALLGENS` — scoped calypr ingest is impossible
  on the postgres lane: `AuthorizeWrite`'s F-B27 project-binding branch
  needs the all-generations auth-path listing that the postgres catalog
  read lane refuses (`ErrAllGenerationsUnsupported`), and the load handler
  wraps it as 403. Owner: arch-backend-09. Evidence:
  `pg-pg-auth-path-discovery-allgens-evidence.md` (session artifacts).
- Arango leg: no ArangoDB service in the worker container; go-arango CI
  lane targets it.
