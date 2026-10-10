# Native Playwright cases for `loom-dev` journeys

`scripts/verify-ui/specs/dev-journeys.spec.mjs` owns the Playwright Test page and
diagnostics fixture. It calls the exported workflow functions in
`scripts/loom-dev.mjs` with that native page. The journey functions retain
their report assertions, direct API/state oracles, fixture seeding, and
evidence paths. They do not launch or close a browser.

| Existing `loom-dev` browser scope | Native Playwright case | Coverage retained |
| --- | --- | --- |
| `verifyJ02BrowserScenario` | `verify-j02` | Route search truncation and choice identity, cancel/tamper draft invariance, pinned and graph-authored routes, source inspection, reload persistence, Preview literals, and request/result correlation. |
| `verifyJ05BrowserScenario` | `verify-j05` | Review blockers, Preview-to-publish receipt binding, Viewer materialization/filter/reload, typed artifact modal/download/ZIP identity, and the `J05_REQUIRED_ASSERTIONS` completion gate. |
| `verifyJ01ExternalBrowserScenario` | `verify-j01` when the selected fixture is external CDA | Patient and Observation profiles, manifest-selected repeated component, immutable source digest, reload, publish/export, typed source evidence, and inspector acknowledgement timings. |
| `verifyJ01BrowserScenario` | `verify-j01` with the default generated-concept fixture | Paged concept discovery, compiler-issued choices, exact saved identities/order, literal zero and absence, owner records, publication, Viewer, and artifact membership. The function delegates to the external case when a manifest is present. |
| `verifyBrowserScenario` | `verify-fast` and `verify-full` | Both retain the complete Builder-to-artifact lifecycle and its assertions. `verify-full` also runs the hot-reload probe. |
| `verifyCurrentBuilderDOM` | `verify-current` | Existing bootstrap workspace titles and rendered values, load-failure detection, hot reload, screenshot/HTML evidence, and browser/API diagnostics. |
| `verifyJ03BrowserScenario` | `verify-j03` | Explicit group selection, row-definition settings, preview, stale-apply refusal, export, and reload persistence. |
| `verifyJ04PatientOperatorScenario` | `verify-j04-patient`, then `verify-j04` | Patient row selection and operator previews, recoding, aggregate/normalization behavior, refusal paths, and literal source/request evidence. The patient-only command keeps its strict no-failure/no-unproven completion rule. |
| `verifyJ04BrowserScenario` | `verify-j04` after the Patient operator case | Browser-authored base columns, table-shape proposals, typed preview, publication, Viewer, artifact schema/rows, and proposal evidence. Its documented evidence limitation remains in the domain report. |

The `verify-*` commands dispatch to the official Playwright CLI with a grep
for the matching native case. `dev`, `dev-rebuild`, `dev-doctor`, and
`dev-down` remain direct API/development utilities. These cases use a separate
native fixture and do not claim the unrelated registry fixture's required
checks. Successful-run screenshots are opt-in with
`LOOM_DEV_CAPTURE_SCREENSHOTS=1`; failure evidence remains captured on failure.

Each prepared journey records its watched source fingerprint and fresh API
build-stamp identity before browser actions, then compares both after the
workflow. Both `verify-current` and `verify-full` run the existing full HMR
probe, owning `ui/packages/loom-ui/src/styles.css` and the exact success/failure
Go probe files derived from the isolated project name. Those Go files must be
absent again, the stylesheet fingerprint must match, the full watched source
fingerprint must be restored, and the recovered API stamp must report the
original source digest. Failure JSON uses the shared bounded native-page and
locator evidence helper.
