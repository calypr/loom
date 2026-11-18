---
name: verify
description: Verify local Loom Builder behavior with focused tests, real-browser e2e cases, independent fixture values, and owned development services.
---

# Verify local Loom development

## Choose the check

Run commands from the repository root. Start with the closest test for the
changed behavior; use the broader commands at a coherent checkpoint.

| Command | Boundary |
|---|---|
| `make test-ui` | UI boundary checks, test type-checking, and Vitest behavior tests. |
| `make test-go` | Ordinary Go packages; environment-gated integration cases may skip. |
| `make test-integration` | Named real Arango/ClickHouse witnesses and isolated Arango profiles. Set `LOOM_TEST_ARANGO_URL`, `LOOM_CLICKHOUSE_URL`, and the ClickHouse database/credentials for owned services. |
| `make e2e` | Local Builder/Viewer flows in real Chrome against the development stack. |

The [feature map](features/README.md) maps browser guarantees to specs in
`tests/e2e/`. A component pass establishes the component boundary. Store
persistence, native interactions, and published Viewer behavior require their
matching integration or browser assertions. Record the command, exit status,
source identity, target identity, fixture, and remaining gaps. A skipped test,
zero matching tests, or old report does not establish a current pass.

For an authenticated external Calypr deployment, use
[verify-loom-ui](../verify-loom-ui/SKILL.md). For ingestion, publication storage,
and GraphQL correctness, use [verify-loom](../verify-loom/SKILL.md).

## Bind the development target

Discover projects with `docker compose ls --all`. Inspect the chosen project
with `docker compose -f compose.dev.yaml -p <project> ps`. Always specify
`compose.dev.yaml`: the default `compose.yaml` describes canonical `loom-demo`.
Read published ports from the running containers; a session file can be stale.
A stopped or missing project is an environment blocker.

```sh
make dev
make dev-doctor
```

The usual development URLs are `http://127.0.0.1:8180` and
`http://127.0.0.1:3180`; isolated projects may use other ports. The fixture is
`tests/fixtures/devloop-fixture`. Source changes use Go and Vite watchers. Use
`make dev-rebuild` after a dependency, toolchain, or development image change.
Require `DEV_DOCTOR_PASSED`; its report binds the Compose identity, fixture
project/generation, API, UI, and source/build identity. Doctor does not prove a
Builder workflow. `make acceptance-real` redeploys canonical services and must
never be used as a passive doctor.

Set `LOOM_DEV_PROJECT` to the seeded project's exact name for a focused browser
spec. Set `LOOM_DEV_API_URL` and `LOOM_DEV_UI_URL` when ports differ from the
defaults. Set `LOOM_E2E_CHROME` to the Chrome/Chromium executable when needed.
For example, after Doctor validates the target:

```sh
node --test --test-concurrency=1 tests/e2e/root-change.spec.mjs
```

## Preflight loaded data

Before a CDA-backed case, verify the exact API, project, generation, and
Explorer. URL-encode each path segment in these production reads:

1. `GET /api/v1/datasets/{project}/generations/{generation}` must return
   `reusable: true` and state `STAGED`, `ACTIVE`, or `READY`.
2. `GET /api/v1/projects/{project}/explorers` must include the chosen Explorer.
3. `GET /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/builder`
   must return at least one `workspace.documents` entry.

Missing or unready data is an environment failure. Synthetic fixture results
do not establish correctness for the current CDA population. Compare exact
scoped rows with an independent source oracle, preserving missing values,
multiplicity, project, generation, and authorization scope.

## Run and diagnose

Freeze watched source during browser verification. Use separate worktrees for
unfinished changes. Record ownership of QA Explorers, fixture mutations,
artifacts, and cleanup before concurrent runs; serialize shared-state writes.
Native input/download checks must use the browser interaction they claim.

Preserve the first complete failure log and exit code. Inspect the first
actionable failure before repeating a broad suite. Repair the owning boundary,
run the smallest discriminating check, then rerun affected acceptance cases on
the final source. Do not raise timeouts without evidence of a timing failure.
Reuse passing evidence when relevant source, configuration, data, and bounds
remain unchanged. A changed binary or watched source invalidates its run.

## Performance escalation

Classify functional correctness and performance separately. A timeout leaves
correctness unverified; a wrong result still requires a functional regression.
Confirm latency failures serially on stable source and unchanged scoped data.
Separate browser wait, compilation, index preparation, query execution, and
rendering before choosing a correction.

After two measured local corrections miss the same budget, profile execution
structure before another rewrite. Escalate sooner for scans, fan-out, large
intermediates, or memory pressure. Record actual input/intermediate counts,
phase timings, indexes, scanned rows, peak memory, and repeated work. Separate
query execution from cursor-draining diagnostics; index entries are not
population counts. Mark unavailable measurements unknown.

Compare strategies using that profile. Preserve null/missing values, empty
parents, multiplicity, membership, authorization, project, and generation.
Keep correctness assertions and the original performance budget. Do not hide
controls, add filters, or raise limits to obtain a pass. Measure the same
operation before and after, then rerun its complete browser lifecycle.

## Owned query probe

`scripts/owned-query-probe.mjs` validates its configured owned Compose Arango
container, project/generation binds, authorization binds, parser result, and
read-only EXPLAIN result. Check its target identity against the actual stack
before use. It defaults to EXPLAIN; an expected-index mismatch blocks execution.

```sh
node scripts/owned-query-probe.mjs \
  --query "$QUERY_FILE" \
  --bind-vars "$BIND_VARS_FILE" \
  --output "$REPORT_DIR/query-probe.json" \
  --expected-index "$EXPECTED_INDEX"
```

Append `--execute` for one bounded query: at most eight seconds and
268435456 bytes. The output path must be fresh. Default reports include source
paths/hashes, plan/scan metadata, and timings, excluding query text, binds,
rows, and raw Arango errors. Keep diagnostic artifacts local.

## Cleanup

The UI exposes `Delete explorer` and the client issues DELETE, but the current
backend does not implement that endpoint. E2E logs this unsupported result and
retains its run-owned Explorer/project; do not claim cleanup succeeded. If a
deployment implements deletion, use it only for run-owned QA Explorers and
verify absence after reloading the list. Keep user-owned Explorers. Project
pruning remains blocked on a backend deletion seam (ARC-E14 in `docs/BACKLOG.md`).

`make dev-down` stops the owned development services and keeps their volumes.
`node scripts/loom-dev.mjs dev-down --purge` removes validated development
volumes when explicitly required. Cleanup validates Compose identity, ports,
source mounts, and labeled volumes; it rejects canonical `loom-demo` and
`NCPI_ACCEPTANCE`. Never remove canonical data as verification cleanup.
Preserve failure evidence and source patches. Remove only task-owned caches
when no longer needed; check free disk before heavyweight work.
