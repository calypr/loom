---
name: verify-loom
description: Drive the Loom API with the real NCPI fixture and verify ingestion, Explorer publication, ClickHouse materialization, and GraphQL reads after behavior changes.
---

# Verify Loom

## Launch

Run `make acceptance-real` from the repository root for one correctness proof.
Docker Compose is the only acceptance target. The command rebuilds and
redeploys canonical `loom-demo` from the current checkout without reseeding or
clearing its data. It then creates a generated `loom-acceptance-<id>` Compose
project, runs the locked fixture there, exports evidence, and removes that
generated project and its volumes.

Run `make acceptance-performance` for a same-machine Git-base comparison. Each
variant gets a generated Compose project, free host ports, run-specific
database namespace, and source-specific image tags. The performance driver
owns teardown of those variants, including interrupted runs. Never use
Kubernetes services, port-forwards, or service containers as acceptance
evidence.

## Doctor

For the complete check, run `make acceptance-real`. After it succeeds, require:

- `.artifacts/acceptance/<id>/report.json` has `status: "PASSED"`;
- `cleanup.json` has `status: "0"` and `cleanup_status: "0"`;
- `cleanup.json` names `loom-demo` as `deployment_project` and a generated
  project as `acceptance_project`;
- `docker compose --project-name loom-demo ps` reports the canonical services
  running; and
- `docker compose ls` contains no generated acceptance project from the run.

`GET http://127.0.0.1:8080/readyz` and the UI root on port `3080` are useful
canonical deployment checks, but they do not replace the isolated fixture
proof.

## Drive

The acceptance command drives the production HTTP surface:

1. Upload every locked per-type NDJSON file to the generated project's API.
2. Publish `testdata/acceptance/ncpi-tcga-brca/workspace.json` through the
   repository Explorer route.
3. Resolve the returned execution ID and read the Explorer viewer state.
4. Query GraphQL metadata, rows, counts, and facets; reject GraphQL errors and
   compare normalized rows with the checked-in oracle digest.
5. Repeat publication and prove the execution and selector are unchanged.
6. Run API and UI smoke checks; local correctness runs include browser smoke by
   default.

## Evidence

Evidence is written to `.artifacts/acceptance/<id>/` and survives generated
project cleanup. Preserve `report.json`, `cleanup.json`, normalized responses,
timings, Compose service/image listings, and browser output when investigating
a failure. The content-addressed fixture cache under
`.cache/acceptance/fixture` is reusable input, not evidence to commit. Reports
exclude credentials and raw upstream payloads outside that local cache.

## Cleanup

Only generated project names may be passed to `demo-down --volumes`. Never run
that command against canonical `loom-demo`; its volumes contain the user's
working data. `acceptance-real` owns cleanup of its generated correctness
project. `acceptance-performance` owns cleanup of its generated variants and
retries cleanup from its EXIT trap after interruption. A cleanup failure fails
the run.

## Helpers

- `make acceptance-real`: redeploy canonical `loom-demo`, then run isolated
  correctness and browser verification.
- `make acceptance-performance`: isolated base/current regression comparison.
- `LOOM_ACCEPTANCE_ARTIFACTS` and `LOOM_ACCEPTANCE_FIXTURE_CACHE`: override the
  evidence and fixture-cache locations.
- `LOOM_ACCEPTANCE_BROWSER_SMOKE=false`: skip browser smoke where Chrome is not
  available; API/UI smoke still runs.
- `go run ./cmd/loom-acceptance --refresh-fixture --fixture-lock <path>`:
  refresh fixture metadata explicitly; review IDs and digests before commit.
