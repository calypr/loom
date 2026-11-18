# Scripts

Run commands from the repository root. Use the repository Go toolchain
(currently 1.26.5), Node 24 as used by CI, and installed UI
dependencies. Docker Compose and Chrome/Chromium are required for local e2e.
CI setup versions and service configuration live in
[tests.yaml](../.github/workflows/tests.yaml).

## Product checks

| Command | What it checks |
|---|---|
| `make test-ui` | UI boundaries, test type-checking, and all Vitest suites. |
| `make test-go` | Ordinary Go tests; environment-gated cases can skip. |
| `make test-integration` | Named real Arango/ClickHouse cases plus isolated Arango profiles. Requires owned `LOOM_TEST_ARANGO_URL`, `LOOM_CLICKHOUSE_URL`, and appropriate ClickHouse database/credentials. |
| `make e2e` | The real-Chrome local Builder/Viewer specs below. |
| `make verify-current` | UI and ordinary Go checkpoint; run integration/e2e for their affected boundaries. |

A green build or zero matching tests does not establish behavior. The named Go
integration routes check actual test completion; do not replace those routes
with an unchecked regex. Use the closest test while editing, then verify the
final integrated source at the relevant acceptance boundary. Preserve complete
failure output before repeating a run. See
[the local verification skill](../.codex/skills/verify/SKILL.md).

## Local browser flows

Start or inspect the owned development stack with `make dev` and
`make dev-doctor`. Require `DEV_DOCTOR_PASSED`. Set `LOOM_DEV_PROJECT` to its
seeded project; set `LOOM_DEV_API_URL`/`LOOM_DEV_UI_URL` if ports differ from
8180/3180. Set `LOOM_E2E_CHROME` when Chrome is not discovered automatically.
Freeze source during each run. Each case owns its QA Explorer. The current
backend rejects Explorer deletion, so cases explicitly report retained projects;
see ARC-E14 in [the backlog](../docs/BACKLOG.md) for the pruning gap.
Reload failures save bounded CDP diagnostics under the system temporary
`loom-e2e-diagnostics` directory; set `LOOM_E2E_DIAGNOSTICS_DIR` to override it.

| Spec | Assertions |
|---|---|
| [author-publish-view](e2e/author-publish-view.spec.mjs) | Native creation, fields, authored Filter, reload, Publish, Viewer rows, and downloaded ZIP. |
| [root-change](e2e/root-change.spec.mjs) | Starting-record replacement, Cancel, Apply, exact rows, and reload. |
| [group-pivot](e2e/group-pivot.spec.mjs) | Fixture-backed Group/Count and Pivot, including Cancel/Apply and reload. |
| [related-field](e2e/related-field.spec.mjs) | Related rows/field, ambiguous ONE rejection, ALL values, saved state, and reload. |
| [record-network](e2e/record-network.spec.mjs) | Patient→Observation traversal and row multiplicity from literal fixture references. |

Run a focused case with:

```sh
node --test --test-concurrency=1 scripts/e2e/root-change.spec.mjs
```

The fixture is `testdata/devloop-fixture`; Patient 001 has two related
Observations. These bounded synthetic cases do not establish full-population
CDA performance, arbitrary FHIR shapes, or authenticated external Calypr
behavior. See [the backlog](../docs/BACKLOG.md) for remaining acceptance gaps.
CI cadence for the e2e job is a separate operator decision.

The component boundary remains available through
`scripts/browser/run.mjs --feature all --out <fresh-directory> --chrome <path>`.
Invoke it with `node`. Its real-Chromium preview/download/route assertions use
controlled callers and responses, so they do not prove the deployed Builder,
real storage, or publication. It records current-run source integrity; retain
that check when changing tooling. `--doctor` checks prerequisites;
`--no-cache` rebuilds the component fixture.

## Node tooling tests

```sh
node scripts/ci/list-node-tests.mjs
```

The selector follows relative dependency closures. Missing or unreadable
imports/fixtures fail discovery; they cannot silently reduce the test list.
Intentional Playwright/browser-package and retired-directory exclusions are
reported by `--explain`. The CI workflow shards the retained `node:test` list.
Run an individual retained test with `node --test <path>`.

Keep boundary, source-integrity, negative-validation, fixture, and cleanup
checks for tools that remain. Do not report excluded or skipped cases as passes.

## Data acceptance and operations

[REAL_DATA_ACCEPTANCE.md](../docs/REAL_DATA_ACCEPTANCE.md) owns
`make acceptance-real` and `make acceptance-performance`. Correctness
acceptance actively redeploys canonical `loom-demo` before using a generated
fixture project; it is not a passive health check. Never delete canonical
volumes as test cleanup. The runbook lists `jq`, `python3`, first-fetch network,
report, performance, and `cleanup.json` requirements.

`make dev-down` keeps owned development volumes. Use
`node scripts/loom-dev.mjs dev-down --purge` only for validated disposable
volumes. The bounded `scripts/owned-query-probe.mjs` defaults to read-only
EXPLAIN and validates its configured owned target; follow the verification
skill before adding `--execute`.

For external authenticated Builder loading, use
[verify-loom-ui](../.codex/skills/verify-loom-ui/SKILL.md). It does not deploy or
mutate the external service. Retired harness disposition and recovery are in
[DELETED_GUARANTEES.md](../docs/verification/DELETED_GUARANTEES.md).
