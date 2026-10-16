# Scripts

Run commands from the repository root.

Native browser cases live under `scripts/verify-ui/specs/`. The case registry
maps each workflow and case name to its Playwright spec at
`scenario.cases[caseName].playwrightTest`. Specs call workflow code under
`scripts/verify-ui/workflows/`; fixtures, request capture, and source oracles
live under `scripts/verify-ui/helpers/`. The main suite uses
`scripts/playwright.config.mjs`.

The native runner and test suite use the existing `scripts` workspace
dependencies. If `scripts/node_modules` is absent, install them with
`npm ci --prefix scripts`.

## Run one native browser case

Run from the repository root with the owned local stack ready. For a
registered case, the runner loads the validated owned environment from its
target config and binds its project and generation to the selected case:

```sh
node scripts/run-native-verification-bracket.mjs \
  --scenario cda-current-draft-membership \
  --case membership \
  --target .codex/owned-cda-target.json
```

The stdout `replayArgv.browser` field is a reference (`referenceOnly: true`)
with the browser executable, exact argv, working directory, and required and
overridden environment variable names. Inherited environment values are
omitted. It does not execute a command; rerun the combined command above with
its target config.

The target config must resolve `sourceRoot` to this checkout. Its validation is
configuration-only and does not prove runtime data identity or expected-value
independence; review the case report and separate source evidence. Before it
runs focused checks, the runner resolves the registered spec and test title,
then uses official Playwright `--list` to require exactly one selected test. A
missing or ambiguous selection is a preparation failure and does not start the
focused checks or stack health stages. The runner writes a fresh `summary.json`
outside watched source. Use
`--help` for an explicit `--grep`, `--target-from-environment` for a case with
no registry identity, or `--checks-only` to run focused checks without Docker
or Playwright. The environment mode requires a validated owned environment and
an explicit selection; registered cases use `--target`. Do not run a second
manual precheck, capture, and health around the same case. The stdout summary
includes focused checks and gaps, failure context, source fingerprint, API
build identity, command timings, and log paths; it links logs instead of
embedding them.

The exact selected native Playwright result and source/API integrity determine
the outcome. A missing or ambiguous selection and a skipped test cannot pass.
Native fixture assertions fail incomplete outcomes, unexpected HTTP errors,
uncaught exceptions, non-cancellation transport errors, and failed or canceled
writes. Read cancellations are diagnostic when required current outcomes still
succeed. Report generation and response-body capture failures are recorded
separately; report statuses, check totals, and request inventories are not
additional pass gates.

## Run selected native cases in one batch

A batch shares one precheck, before and after captures, and before and after
health checks across an explicit selection. Save a schema-versioned manifest,
for example:

```json
{
  "schemaVersion": 1,
  "cases": [
    { "scenario": "builder-controls", "case": "first-table", "target": { "mode": "environment" } },
    { "scenario": "builder-load", "case": "list", "target": { "mode": "environment" } }
  ]
}
```

Run it with:

```sh
node scripts/run-native-verification-bracket.mjs --batch /tmp/native-cases.json --concurrency 2
```

Use environment targets for cases without a registered target identity and
config targets for cases with one: `{ "mode": "environment" }` or
`{ "mode": "config", "path": "..." }`. All entries must resolve to the
same capture identity, including project and generation, source root, runtime
endpoints, containers, and mapped target environment. Incompatible targets
must run in separate batches. The manifest selects registered cases; it is not
a second work queue and accepts no arbitrary test arguments. Concurrency
defaults to 1 and accepts 1 through 3. Only cases marked in the registry for
fresh fixture projects can overlap; other cases run alone. Those marked
fixtures create and validate their own projects. A failed case does not
suppress later ready cases, while shared evidence failure prevents a passing
batch verdict.
The batch summary points to each case summary and the single shared bracket.
The existing single-case commands above remain available.

For diagnosis, the low-level components are
`owned-stack-verification.mjs` (`--mode precheck|health`) and
`capture-owned-verification.mjs` (`--phase before|after`). Before capture also
requires the precheck artifact; both phases write four distinct source, docs,
API, and mount artifacts. The normal browser workflow is the combined runner
above.

For a single case, `summary.durationMs` measures that bracket invocation. A
batch summary measures the full shared batch interval; case summaries retain
their Playwright command durations. To report observed intervals and optional
human phase spans, pass each summary explicitly:

```sh
node scripts/verification-timing.mjs report \
  --summary /tmp/native-batch/batch-summary.json \
  --summary /tmp/native-batch/case-1/summary.json \
  --summary /tmp/native-batch/case-2/summary.json \
  --spans /tmp/verification-spans
```

Replace these example paths with the `summary` and `cases[*].summaryPath`
values from the batch command output.

The report uses batch summaries for the shared window and case summaries for
browser command durations. Record a phase with `start`, then copy its returned
span ID into `finish`:

```sh
node scripts/verification-timing.mjs start --spans /tmp/verification-spans --owner "reviewer" --case-id "issue-123" --phase review
node scripts/verification-timing.mjs finish --spans /tmp/verification-spans --span-id "copy-span-id-here"
```

It does not discover files recursively or infer human time from gaps. Without
recorded phase spans, diagnosis, implementation, review, and integration
remain unattributed.

After partial staging from a dirty worktree, verify the exact staged or
committed snapshot; a dirty-worktree pass does not prove the delivered artifact.
See the [verification skill](../.codex/skills/verify/SKILL.md) for the required check.

Run the main suite with:

```sh
./scripts/node_modules/.bin/playwright test --config scripts/playwright.config.mjs
```

Run the static migration gates after installing the existing UI and scripts
workspaces with `npm ci --prefix ui` and `npm ci --prefix scripts`:

```sh
node scripts/maintenance/playwright/check-native-playwright.mjs
node scripts/maintenance/playwright/check-standalone-playwright-ledger.mjs
node scripts/maintenance/playwright/check-playwright-migration.mjs --check
node scripts/maintenance/playwright/check-browser-eval-returns.mjs
```

The mapping and ledger gates check file mappings and migration bookkeeping.
They do not establish a passing browser lifecycle. The migration gate finds
remaining legacy browser machinery. The return checker audits value-consuming
browser evaluation calls. Use
`node scripts/maintenance/playwright/check-browser-expression-syntax.mjs <driver.mjs>`
to compile browser expressions from one driver. The manifest builder accepts
explicit source and discovery inputs; inspect its `--help` before rebuilding the
conversion ledger.

Construction-preview measurements live in
`scripts/measurements/construction-preview/`. Keep their benchmark reports
separate from browser-case lifecycle evidence.
