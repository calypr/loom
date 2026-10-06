# Scripts

Run commands from the repository root.

Native browser cases live under `scripts/verify-ui/specs/`. The case registry
maps each workflow and case name to its Playwright spec at
`scenario.cases[caseName].playwrightTest`. Specs call workflow code under
`scripts/verify-ui/workflows/`; fixtures, request capture, and source oracles
live under `scripts/verify-ui/helpers/`. The main suite uses
`scripts/playwright.config.mjs`.

## Owned-stack precheck, capture, and health

Set `REPORT_DIR` to a fresh run-specific directory, then source the validated
`LOOM_CDA_*` environment. Use the canonical precheck and health CLI around
`capture-owned-verification.mjs`. This keeps Docker stamp and owned-target
checks in repository code instead of copying temporary helpers into each run
directory:

```sh
mkdir -p "$REPORT_DIR"
node scripts/owned-stack-verification.mjs --mode precheck --output "$REPORT_DIR/api-build-precheck.json"
node scripts/capture-owned-verification.mjs --phase before \
  --precheck-input "$REPORT_DIR/api-build-precheck.json" \
  --source-output "$REPORT_DIR/source-before.json" \
  --docs-output "$REPORT_DIR/docs-before.json" \
  --api-output "$REPORT_DIR/api-identity-before.json" \
  --mount-output "$REPORT_DIR/owned-mounts-before.json"
node scripts/owned-stack-verification.mjs --mode health \
  --output "$REPORT_DIR/health-before.json" \
  --identity "$REPORT_DIR/api-identity-before.json"
```

The capture coordinator alone performs Docker-backed precheck, capture, and
health operations. Keep the watched source and running API unchanged from the
before capture through the after capture and final health check. Before-phase
capture requires the precheck artifact, verifies its fresh/current-source flags
and owned API container, then confirms its three-part identity still matches
the captured API. It records that checked precheck in
`api.apiBuildPrecheck`. The durable closure's
`integrityClosure.apiBuildIdentity.precheck` must come from
`api.apiBuildPrecheck.apiBuildIdentity` in the before API artifact.

After the browser run, capture with `--phase after` and four distinct
`*-after.json` paths, then run health mode again with the same before-capture
identity via `--identity`. See the [verification skill](../.codex/skills/verify/SKILL.md)
for the complete before/after sequence and acceptance rules.

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
