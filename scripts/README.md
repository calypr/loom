# Scripts

Run commands from the repository root.

Native browser cases live under `scripts/verify-ui/specs/`. The case registry
maps each workflow and case name to its Playwright spec. Specs call workflow
code under `scripts/verify-ui/workflows/`; fixtures, request capture, and source
oracles live under `scripts/verify-ui/helpers/`. The main suite uses
`scripts/playwright.config.mjs`.

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
