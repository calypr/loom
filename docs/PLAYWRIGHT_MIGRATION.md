# Browser verifier migration inventory

This inventory describes the verifier source at checkpoint `07573db4`. A passing historical report is evidence for its recorded source and build only. Use the current report fingerprint and API build identity before claiming verification of another checkout.

| Driver family | Current callers | Browser machinery | Migration status |
| --- | --- | --- | --- |
| `scripts/verify-ui/browser.mjs` | Nine runtime modules under `scripts/verify-ui`, including `common.mjs` and `workflows.mjs`; five registered entrypoints and 13 named cases | Wraps the Chrome/CDP launcher in `scripts/loom-dev.mjs`; implements click, fill, waits, network monitoring, and DOM capture | `builder-authoring/authoring`, `builder-authoring/suggestions`, and both `builder-load` cases use Playwright. Nine registered cases still use CDP. Migrate those before deleting this module. |
| `scripts/lib/browser.mjs` | Remaining `scripts/verify-cda-*-browser.mjs`, standalone browser scripts, and its tests | Separate Chrome/CDP launcher, custom actions, selection, evaluation, and request listeners | Pivot reload, disclosure actionability, and last-table now use Playwright. Migrate remaining callers before deleting this module. The migration gate reports all remaining direct CDP users. |
| `scripts/loom-dev.mjs` | `make verify-fast`, `make verify-full`, and the `verify-ui` adapter | Integrated legacy Chrome/CDP driver | Its own browser actions still need migration. The Playwright authoring cases reuse its owned-stack validation and fixture setup without calling its browser driver. |

The two adapter modules have different APIs. `verify-ui` owns assertion/report integration, injected faults, and action timing. CDA scripts retain independent raw fixture or CDA queries and source/build freezes. Playwright replaces their interaction, navigation, waiting, and browser event plumbing; it does not replace those oracles.

Run `node scripts/check-playwright-migration.mjs` for the current per-file
inventory. `node scripts/check-playwright-migration.mjs --check` fails until
all Chrome/CDP callers and replaced drivers are removed. At this checkpoint,
71 `.mjs` files remain. A zero count is a migration gate, not proof of browser
correctness; each case still needs its required assertions and owned runtime
evidence.

## Evidence contract for each migrated case

Record the visible entry path and control, expected visible rows or state, independent source oracle, exact project/generation/authorization scope, preview and persistence lifecycle, action-to-render duration, source fingerprint, API build identity, and report path. A failed action must retain the locator and control state, screenshot, DOM, console and owned request diagnostics, and a failure trace. Missing assertions, skipped paths, unexpected browser errors, and unexpected API failures remain unsuccessful.

| Case | User path and expected visible result | Independent oracle | Lifecycle and evidence |
| --- | --- | --- | --- |
| `builder-authoring/authoring`, small fixture | Open Builder, create a blank Explorer and Patient table, add Gender through the visible editor, inspect the automatic Preview, Publish, then reload Builder; both Patient IDs and both configured fields must remain visible | The independent `testdata/devloop-fixture/Patient.ndjson` IDs under a fresh project and `fixture-v1` generation | Passed before the CDA extension at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu1ldsq-fde5d18.json`. This is historical evidence for that source snapshot. The current CDA stack cannot be targeted as this small fixture without changing its owned session configuration. |
| `builder-authoring/authoring`, CDA | Open an owned CDA Builder, create a blank Explorer and Patient table, add `identifier[].value` with ALL through the visible choice dialog, inspect the 25-row Preview, Publish, then reload; the configured field must persist | The read-only 159,047-Patient `CDA-FHIR/META/Patient.ndjson` file, exact first 25 identities ordered by storage key, and each row's raw identifier list including duplicates; file SHA-256, project and `cda-fhir-v1` generation are recorded | Correctness, Publish response, and reload persistence passed; overall **failed** because Publish rendered in 14,690 ms against the 5,000 ms gate. First-failure screenshot, DOM, control state, sanitized diagnostics, and Playwright trace are under `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-authoring-muu325aa-18d89e9/`; report is the matching `.json`. No Publish retry occurs within a case. |
| `builder-authoring/suggestions`, CDA | Open an owned CDA Builder, create a Patient table, expand Raw FHIR fields, and select the exact Patient ID checkbox | The same independent CDA Patient source and its 25-row preview window, plus the rendered catalog candidate labels | Passed with nine Patient field controls and no unexpected browser/API errors at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-suggestions-muu373p7-d238b59.json`. This case checks discoverability and actionability; it does not Apply, Publish, or reload. |
| `builder-authoring/cohort-recode`, CDA | Create a Patient table and exact two-member cohort, change Patient ID values to one shared category, switch ALL to ONE, reload, edit back to ALL, remove the recoding, and reload again; Preview must show the exact member values at each step | Streamed raw CDA Patient IDs, exact selection revision membership and project/generation, saved column bindings, and independent Builder/Preview API reads | Passed 78 assertions at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-authoring-cohort-recode-muu7mtxt-bcfdd13.json`. Three catalog requests canceled by the UI on their exact panel transitions are retained with request identity; all other browser/API failures remain fatal. Source and build stayed fixed. Run `node scripts/verify-ui/builder-authoring.mjs --case cohort-recode --reuse-owned-dataset` with the isolated CDA environment below. |
| `builder-load/list`, CDA | Open the owned CDA Builder, inject one list-read failure, observe the error alert, click its unique enabled Try again button, and see the same selected Patients table with ready preview | Exact UI-proxy GET origin, project, path, method, and one-shot failure; the independently validated loaded CDA generation and bootstrap Explorer | Passed at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-load-list-muu4vpps-6abd603.json`. Retry-to-render was 194 ms; source and API build identity remained unchanged. The two earlier failed harness runs and first-failure traces remain retained beside their reports. |
| `builder-load/state`, CDA | Open the same Builder, inject one builder-state read failure, observe the error alert, click Try again, and see the selected Patients table with ready preview | Exact UI-proxy GET origin, project, Explorer, path, method, and one-shot failure; the independently validated loaded CDA generation | Passed at `.artifacts/loom-dev/c52d4223d857/verify-ui/builder-load-state-muu4w81m-033763d.json`. Retry-to-render was 1,585 ms; source and API build identity remained unchanged. |
| `verify-browser-disclosure.mjs` | In an isolated page, collapse Advanced through its summary, prove the hidden input cannot be filled, reopen it, and type into the visible editable input | Native Playwright visibility/actionability and the literal input value after the visible action | Passed at `/private/tmp/loom-playwright-disclosure-evidence/report.json`. This is a harness actionability check, not a Loom product lifecycle. |
| `verify-cda-add-column-dialog.mjs` | Open the owned CDA Patient Builder, enter Add columns → Fields and related data → Coded values, open each of three distinct coded suggestions, Cancel its route dialog, and confirm each suggestion returns | Exact saved workspace, draft version and digest read independently before and after each Cancel; semantic suggestion names identify the controls | Passed at `/private/tmp/loom-playwright-add-column-dialog-evidence-rerun12/report.json`: three dialogs, 21 transitions with maximum 1,174 ms, unchanged source/build, and zero unexpected HTTP or network failures. Fourteen expected UI cancellations retain exact request/action classification. This case checks dialog actionability and Cancel; it does not Apply a column. Run `node scripts/verify-cda-add-column-dialog.mjs <explorer-id> <evidence-dir>` with explicit owned CDA target variables. |
| `verify-cda-last-table.mjs` | Open an owned CDA Specimen Builder, delete its only table, Undo, then reload | Exact saved workspace revision and first 25 ordered Preview rows from 742,505 independent CDA Specimen source identities | Passed at `/private/tmp/loom-playwright-last-table-evidence-rerun4/report.json`; Delete, Undo, and reload met the five-second limit with unchanged source/build. Earlier failed evidence is retained in sibling directories. |
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
