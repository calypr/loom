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

For diagnosis, the low-level components are
`owned-stack-verification.mjs` (`--mode precheck|health`) and
`capture-owned-verification.mjs` (`--phase before|after`). Before capture also
requires the precheck artifact; both phases write four distinct source, docs,
API, and mount artifacts. The normal browser workflow is the combined runner
above.

`summary.durationMs` measures the full bracket process, and each
`commands[*].durationMs` measures a child process. They do not measure human
environment setup, diagnosis, implementation, review, or integration; record
those phases separately or mark them unknown.

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
