# Browser verifier migration inventory

This inventory describes the verifier source at checkpoint `07573db4`. A passing historical report is evidence for its recorded source and build only. Use the current report fingerprint and API build identity before claiming verification of another checkout.

| Driver family | Current callers | Browser machinery | Migration status |
| --- | --- | --- | --- |
| `scripts/verify-ui/browser.mjs` | Nine runtime modules under `scripts/verify-ui`, including `common.mjs` and `workflows.mjs`; five registered entrypoints and 13 named cases | Wraps the Chrome/CDP launcher in `scripts/loom-dev.mjs`; implements click, fill, waits, network monitoring, and DOM capture | `builder-authoring/authoring` and `builder-authoring/suggestions` use Playwright. Eleven registered cases still use CDP. Migrate those before deleting this module. |
| `scripts/lib/browser.mjs` | 39 remaining `scripts/verify-cda-*-browser.mjs`, ten other production scripts, and its tests | Separate Chrome/CDP launcher, custom actions, selection, evaluation, and request listeners | The Pivot reload case now uses Playwright. Migrate remaining callers before deleting this module. Thirty-nine remaining CDA scripts call `cdp.send` directly; one dispatches raw drag events. A helper swap alone would leave browser driving on CDP. |
| `scripts/loom-dev.mjs` | `make verify-fast`, `make verify-full`, and the `verify-ui` adapter | Integrated legacy Chrome/CDP driver | Its own browser actions still need migration. The Playwright authoring cases reuse its owned-stack validation and fixture setup without calling its browser driver. |

The two adapter modules have different APIs. `verify-ui` owns assertion/report integration, injected faults, and action timing. CDA scripts retain independent raw fixture or CDA queries and source/build freezes. Playwright replaces their interaction, navigation, waiting, and browser event plumbing; it does not replace those oracles.

## Evidence contract for each migrated case

Record the visible entry path and control, expected visible rows or state, independent source oracle, exact project/generation/authorization scope, preview and persistence lifecycle, action-to-render duration, source fingerprint, API build identity, and report path. A failed action must retain the locator and control state, screenshot, DOM, console and owned request diagnostics, and a failure trace. Missing assertions, skipped paths, unexpected browser errors, and unexpected API failures remain unsuccessful.

| Case | User path and expected visible result | Independent oracle | Lifecycle and evidence |
| --- | --- | --- | --- |
| `builder-authoring/authoring`, small fixture | Open Builder, create a blank Explorer and Patient table, add Gender through the visible editor, inspect the automatic Preview, Publish, then reload Builder; both Patient IDs and both configured fields must remain visible | The independent `testdata/devloop-fixture/Patient.ndjson` IDs under a fresh project and `fixture-v1` generation | Passed before the CDA extension at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu1ldsq-fde5d18.json`. This is historical evidence for that source snapshot. The current CDA stack cannot be targeted as this small fixture without changing its owned session configuration. |
| `builder-authoring/authoring`, CDA | Open an owned CDA Builder, create a blank Explorer and Patient table, add `identifier[].value` with ALL through the visible choice dialog, inspect the 25-row Preview, Publish, then reload; the configured field must persist | The read-only 159,047-Patient `CDA-FHIR/META/Patient.ndjson` file, exact first 25 identities ordered by storage key, and each row's raw identifier list including duplicates; file SHA-256, project and `cda-fhir-v1` generation are recorded | Correctness, Publish response, and reload persistence passed; overall **failed** because Publish rendered in 14,690 ms against the 5,000 ms gate. First-failure screenshot, DOM, control state, sanitized diagnostics, and Playwright trace are under `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu325aa-18d89e9/`; report is the matching `.json`. No Publish retry occurs within a case. |
| `builder-authoring/suggestions`, CDA | Open an owned CDA Builder, create a Patient table, expand Raw FHIR fields, and select the exact Patient ID checkbox | The same independent CDA Patient source and its 25-row preview window, plus the rendered catalog candidate labels | Passed with nine Patient field controls and no unexpected browser/API errors at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-suggestions-muu373p7-d238b59.json`. This case checks discoverability and actionability; it does not Apply, Publish, or reload. |
| `verify-cda-pivot-reload-browser.mjs` | Open an owned saved wide Pivot table and sweep the horizontally virtualized preview across five reloads; every category column and cell must retain its exact value | The owned seed report's raw CDA category witnesses, checked against the saved Pivot category keys and stable output column IDs | Read-only reload and saved draft equality. Set `LOOM_CDA_API_ORIGIN`, `LOOM_CDA_UI_ORIGIN`, `LOOM_CDA_API_CONTAINER`, and `LOOM_PIVOT_RELOAD_SEED` to an isolated CDA target, then run `node scripts/verify-cda-pivot-reload-browser.mjs <evidence-dir>`. The report is `<evidence-dir>/report.json`; this branch has the CDA dataset now, but no owned 31-category Pivot seed report. Live replay remains untested. |

For the loaded CDA generation on this branch's isolated stack, run either
migrated Builder case with `--case authoring` or `--case suggestions`:

```bash
LOOM_DEV_SOURCE_ROOT=/private/tmp/loom-playwright-verification \
LOOM_DEV_COMPOSE_PROJECT=loom-dev-c52d4223d857 \
LOOM_DEV_API_PORT=8282 LOOM_DEV_UI_PORT=30102 \
LOOM_DEV_PROJECT=loom_dev_cda_playwright_c52d4223d857 \
LOOM_DEV_GENERATION=cda-fhir-v1 \
LOOM_DEV_FIXTURE_DIR=/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META \
node scripts/verify-ui/builder-authoring.mjs --case authoring --reuse-owned-dataset
```

Run `node scripts/loom-dev.mjs dev-doctor` first with the same environment.
The named target must report `DEV_DOCTOR_PASSED`. The cases reuse the loaded
generation, create fresh Explorers, and keep reports under this checkout's
`.artifacts` directory.

## Known documentation and coverage gaps

- A historical run note in `docs/UI_VERIFICATION.md` names `make verify-ui-test`, but the Makefile has no such target. The runnable test command is `node --test scripts/verify-ui/tests/*.test.mjs` after `npm ci --prefix scripts`.
- The current feature guides include reports and commands tied to `/private/tmp/loom-construction-implementation` and its CDA stack. Those reports are historical and cannot prove this branch.
- `make verify-fast` and `make verify-full` exercise the older synthetic fixture. They do not establish the CDA lifecycle or the required project, generation, and authorization semantics.
- A registered case proves only its declared assertions. Compare assertions with the original case and inspect actual result predicates before marking its migration complete.
- CDA replay scripts that require owned seed reports remain untested until their corresponding 31-category Explorers are built in this isolated project. The loaded CDA generation alone does not establish a Pivot lifecycle pass.

## Primary references

- [Playwright actionability](https://playwright.dev/docs/actionability), [best practices](https://playwright.dev/docs/best-practices), [trace viewer](https://playwright.dev/docs/trace-viewer), and [test agents](https://playwright.dev/docs/test-agents).
- [Verification skill example](https://github.com/poteto/verification-skill-example) is a fictional feature map and omits its driver.
- [pstack verification skill guidance](https://github.com/cursor/plugins/tree/main/pstack/skills) informs skill upkeep and live evidence requirements.
