# Browser verifier migration inventory

This inventory describes the verifier source at checkpoint `07573db4`. A passing historical report is evidence for its recorded source and build only. Use the current report fingerprint and API build identity before claiming verification of another checkout.

| Driver family | Current callers | Browser machinery | Migration status |
| --- | --- | --- | --- |
| `scripts/verify-ui/browser.mjs` | Nine runtime modules under `scripts/verify-ui`, including `common.mjs` and `workflows.mjs`; five registered entrypoints and 13 named cases | Wraps the Chrome/CDP launcher in `scripts/loom-dev.mjs`; implements click, fill, waits, network monitoring, and DOM capture | Existing cases use CDP. Migrate case by case while retaining registry requirements and independent fixture comparisons. |
| `scripts/lib/browser.mjs` | 39 remaining `scripts/verify-cda-*-browser.mjs`, ten other production scripts, and its tests | Separate Chrome/CDP launcher, custom actions, selection, evaluation, and request listeners | The Pivot reload case now uses Playwright. Migrate remaining callers before deleting this module. Thirty-nine remaining CDA scripts call `cdp.send` directly; one dispatches raw drag events. A helper swap alone would leave browser driving on CDP. |
| `scripts/loom-dev.mjs` | `make verify-fast`, `make verify-full`, and the `verify-ui` adapter | Integrated legacy Chrome/CDP driver | Synthetic fixture coverage only. Its browser actions also need migration; the CDA cases do not call this family directly. |

The two adapter modules have different APIs. `verify-ui` owns assertion/report integration, injected faults, and action timing. CDA scripts retain independent raw fixture or CDA queries and source/build freezes. Playwright replaces their interaction, navigation, waiting, and browser event plumbing; it does not replace those oracles.

## Evidence contract for each migrated case

Record the visible entry path and control, expected visible rows or state, independent source oracle, exact project/generation/authorization scope, preview and persistence lifecycle, action-to-render duration, source fingerprint, API build identity, and report path. A failed action must retain the locator and control state, screenshot, DOM, console and owned request diagnostics, and a failure trace. Missing assertions, skipped paths, unexpected browser errors, and unexpected API failures remain unsuccessful.

| Case | User path and expected visible result | Independent oracle | Lifecycle and evidence |
| --- | --- | --- | --- |
| `builder-authoring/authoring` | Open Builder, create a blank Explorer and Patient table, add Gender through the visible editor, inspect the automatic Preview, Publish, then reload Builder; both Patient IDs and both configured fields must remain visible | The independent `testdata/devloop-fixture/Patient.ndjson` IDs under the run's fresh project and `fixture-v1` generation | Native authoring, automatic Preview, Publish, and reload pass on the isolated stack. Run `node scripts/verify-ui/builder-authoring.mjs --case authoring` with explicit named dev-session variables. The passing report is `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu1ldsq-fde5d18.json`; first-failure reports from earlier harness corrections are retained beside it. Preview and Publish did not require a manual retry. |
| `verify-cda-pivot-reload-browser.mjs` | Open an owned saved wide Pivot table and sweep the horizontally virtualized preview across five reloads; every category column and cell must retain its exact value | The owned seed report's raw CDA category witnesses, checked against the saved Pivot category keys and stable output column IDs | Read-only reload and saved draft equality. Set `LOOM_CDA_API_ORIGIN`, `LOOM_CDA_UI_ORIGIN`, `LOOM_CDA_API_CONTAINER`, and `LOOM_PIVOT_RELOAD_SEED` to an isolated CDA target, then run `node scripts/verify-cda-pivot-reload-browser.mjs <evidence-dir>`. The report is `<evidence-dir>/report.json`; this branch's small dev fixture cannot satisfy the 31-category prerequisite. |

For the isolated development stack used by this branch, run the migrated
authoring case with:

```bash
LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-playwright-verification \
LOOM_DEV_COMPOSE_PROJECT=loom-dev-c52d4223d857 \
LOOM_DEV_API_PORT=8282 LOOM_DEV_UI_PORT=30102 \
LOOM_DEV_PROJECT=loom_dev_c52d4223d857 \
node scripts/verify-ui/builder-authoring.mjs --case authoring
```

Run `node scripts/loom-dev.mjs dev-doctor` first from this checkout. The
named target must report `DEV_DOCTOR_PASSED`. The case creates a fresh
verification project and keeps its report under this checkout's `.artifacts`.

## Known documentation and coverage gaps

- A historical run note in `docs/UI_VERIFICATION.md` names `make verify-ui-test`, but the Makefile has no such target. The runnable test command is `node --test scripts/verify-ui/tests/*.test.mjs` after `npm ci --prefix scripts`.
- The current feature guides include reports and commands tied to `/private/tmp/loom-construction-implementation` and its CDA stack. Those reports are historical and cannot prove this branch.
- `make verify-fast` and `make verify-full` exercise the older synthetic fixture. They do not establish the CDA lifecycle or the required project, generation, and authorization semantics.
- A registered case proves only its declared assertions. Compare assertions with the original case and inspect actual result predicates before marking its migration complete.
- CDA replay scripts that require owned seed reports or a loaded CDA dataset cannot run against the small isolated development fixture. Until an isolated CDA seed is available, report those cases as untested here, even when a Playwright actionability test passes.

## Primary references

- [Playwright actionability](https://playwright.dev/docs/actionability), [best practices](https://playwright.dev/docs/best-practices), [trace viewer](https://playwright.dev/docs/trace-viewer), and [test agents](https://playwright.dev/docs/test-agents).
- [Verification skill example](https://github.com/poteto/verification-skill-example) is a fictional feature map and omits its driver.
- [pstack verification skill guidance](https://github.com/cursor/plugins/tree/main/pstack/skills) informs skill upkeep and live evidence requirements.
