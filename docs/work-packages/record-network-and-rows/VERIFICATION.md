# GRAPH-ROWS verification record

## Follow-up 08–11: integrated implementation, acceptance incomplete

The required planning revision `9613310fefacc987933275b9a9f92f3e456e886a` was
fetched and verified on the requested branch. Existing work was preserved. Current
source is not yet a completed acceptance snapshot; any layout repair requires
rechecking affected source identities and guarantees. The original 01–07 record
below belongs to `58544dcbb` and its explicitly recorded snapshots. Its imported
and ordinary passes do not prove the complementary-editor follow-up.

| Current check | Result and scope | Raw evidence |
| --- | --- | --- |
| Registered draft behavior | 210 passed, 0 failed/skipped, 19 registered suites, 68.503s, process exit 0; production components/functions with JSDOM and mocked services, no live persistence proof | `work/graph-rows-followup/drafts-final-inspector-20261113T0815/` |
| Full root UI build after inspector integration | actual exit 0; Vite stages 30.07s and 23.44s; chunk-size warning remains. A later final build is required after the native repair batch | `/tmp/loom-followup-final-inspector-build.log` |
| Full test TypeScript | actual exit 0 | `/tmp/loom-followup-final-210-types.log` |
| Full authoringv2/lifecycle | exit 0, 0.391s / 22.259s; all 200 main Go package files byte-equal to tested copy | `/workspace/loom-followup-candidate-review/work/review-candidate/final-authoring-lifecycle-full.{log,exit}`, `final-go-package-manifest.json` |
| Candidate HTTP/root equality | four HTTP candidate contracts pass (0.285s); exact entire root candidate versus Apply PRESERVE/REPLACE passes (0.097s) | `/workspace/loom-followup-candidate/work/graph-rows-followup/root-final-evidence.json` |
| Real-Arango list expansion | retained ALL Observation and independent Condition lists over five/six pairs, distinct explicitly selected per-match scalar; pass (0.30s) | `/workspace/loom-followup-candidate/work/graph-rows-followup/row-candidate-and-list-checks.json`, `list-expansion-owned-container.log` |
| Owned demo shell layout repair | demo-only CSS fix independently reviewed; coordinator red/green reproduction passes after repair; demo build exit 0 (Vite 21.02s). Source package unchanged, so 210-test and package-build evidence remains applicable. Native acceptance still pending | `/workspace/loom-followup-layout-review/work/review-layout/receipt-demo-shell.md`; `/tmp/loom-followup-demo-coordinator-{red,green}` |
| Native graph-first attempt 1 | failed before authoring: initial 1536×900 document vertically overflowed; exit 1, 33.209s wrapper; no lifecycle pass | `/tmp/loom-followup-graph-first-1/` |
| Native graph-first attempt 2 | all nine idle size/presentation captures pass after shell repair; selected occurrence inspector clips to 71.16% inside the graph pane; exit 1, 54.053s wrapper / 48.739s test; no lifecycle pass | `/tmp/loom-followup-graph-first-2/`; independent read-only reproduction `/tmp/loom-followup-inspector-coordinator-red-3/` |
| Inspector portal and table navigation | narrow review and 59 focused tests pass; real inspector geometry at 900/1100/back and independent coordinator green pass; full native scope remains pending | `/workspace/loom-followup-root-review/work/review-inspection/receipt.md`; `/tmp/loom-followup-portal-geometry-green-1/geometry.json`; `/tmp/loom-followup-inspector-coordinator-green` |
| Native graph-first attempt 3 | all 13 layout captures and candidate/shared-owner checks pass; fails saved preview recovery after A→B→A and Cancel; exit 1, 75.460s; no complete case pass | `/tmp/loom-followup-graph-first-3/` |
| Saved-preview recovery repair | one-line reducer preview-generation fix; coordinator three red regressions then 27 green; full test TypeScript exit 0; unchanged saved workspace and real native prefix red/green verified | `/workspace/loom-followup-root-review/work/review-table-preview/receipt.md`; `/tmp/loom-table-preview-coordinator-green.log`; `/tmp/loom-followup-preview-coordinator-{red,green}/result.json` |
| Native graph-first attempt 4 | Cancel recovery, connection Apply/required edit/reload pass; harness incorrectly assumes projection.occurrenceId; exit 1, 45.874s; no complete case pass | `/tmp/loom-followup-graph-first-4/`; `/tmp/loom-observation-provenance-coordinator.json` |
| Observation resolver harness repair | uses unique EXACT column binding → sourceStepId, rejects ambiguous/unknown metadata; eight focused tests and real metadata proof pass; exact value oracles unchanged | `/workspace/loom-followup-native-review/work/review-native/observation-resolver-receipt.md` |
| Native graph-first attempt 5 | independent ALL list oracle fails: actual [13, 11], expected [11, 13]; exit 1, 64.846s; no normalization or complete case pass | `/tmp/loom-followup-graph-first-5/`; `/tmp/loom-relationship-order-coordinator-red.json` |
| Contributor ordering repair | integrated and reviewed: logical_key with storage-key fallback/tie-breaker; compact/projected/prepared provenance retained. Compiler 20; historical 19 readable, execution rejected, cache separated. All 1,272 Go files main/writer equal; full legacy fixture plus six real-Arango generation/insertion-order cases pass; parent six cases pass (0.87s), four HTTP contracts pass (0.090s) | `/workspace/loom-followup-candidate-review/work/review-candidate/order-final/receipt.md`; `/tmp/loom-compiler20-order-coordinator-green.log`; `/tmp/loom-compiler20-http-contracts.log` |
| Imported ordering disclosure | narrow conditional-copy review, two existing component files/ten tests pass. Paragraph must ship only with the coupled validated backend ordering contract | `/workspace/loom-followup-root-review/work/review-ordering-copy/receipt.md`; `/tmp/loom-order-disclosure-tests.log` |
| Affected-field readability and row focus | exact display/focus deltas independently reviewed; four focused files/40 tests pass (21.19s), full test TypeScript exit 0; technical IDs remain in closed disclosure | `/workspace/loom-followup-native-review/work/review-native/source-labels-receipt.md`; `/workspace/loom-followup-root-review/work/review-row-focus/receipt.md`; `/tmp/loom-row-focus-labels-final{.log,-types.log}` |
| Native graph-first attempt 6 | setup-only empty Docker stdout guard failure before actions; configuration/source reads matched; not a product pass | `/tmp/loom-followup-graph-first-6/` |
| Scoped whole graph/row passes | graph attempts 7/8/9 pass (143.900s/59.664s/69.574s); row attempts 2/3 pass (43.986s/61.639s), after row attempt 1 focus failure (83.114s). These belong to their recorded earlier harness/source identities | `/tmp/loom-followup-{graph-first-7,graph-first-8,graph-first-9,row-first-1,row-first-2,row-first-3}/` |
| Strict graph/row equivalence | graph 9 and row 3 whole cases have equal canonical semantics and three exact rows with 17 layout captures; parent independently reran comparison. Explicit unique actual catalog mapping fixes generated-edge identity mismatch; no value oracle normalization | `/tmp/loom-followup-graph-first-9/canonical-equivalence.json`; `/workspace/loom-followup-native-review/work/review-native/catalog-equivalence-receipt.md` |
| Imported selector repair | imported attempt 1 failed obsolete row selector after proving five default output IDs (59.990s). Five scoped row locators/three Group dialog checks repaired; no product/oracle/deadline changes; saved-state-unchanged Group diagnostic passes | `/workspace/loom-followup-native-review/work/review-native/source-row-selector-expanded-receipt.md`; `/tmp/loom-source-row-group-coordinator-green` |
| Current imported whole case | PASS, 77.747s; all five outputs, overlay lifecycle, immutable pins, real two-editor 409 and explicit stale-owner Cancel, no ADD_ROUTE | `/tmp/loom-followup-imported-2/{command.json,native-evidence.json}` |
| First-table metadata scheduling repair | reviewed one-action scheduling gate skips intermediate CREATE/ADD metadata without claiming network ownership; 37 focused tests and full test TypeScript pass. Parent controlled native red/green proves fresh final identity loading; no 409 waiver | `/workspace/loom-followup-root-review/work/review-first-table-network/receipt.md`; `/tmp/loom-ordinary-network-race/{red-coordinator,green-coordinator-2}` |
| Ordinary attempts 1–2 | attempt 1 fails initial metadata race (21.963s). After the scheduling fix, attempt 2 completes functional actions but fails strict semantic-inventory assertion on a 409 after REMOVE_ROUTE (26.780s); incomplete, not an ordinary whole-case pass | `/tmp/loom-followup-ordinary-{1,2}/` |
| Closed Advanced catalog visibility repair | reviewed minimal gate prevents closed setup from mounting either background ConceptCatalog; root picker/main Add Columns/other forms unchanged. Two actual Builder tests pass (8.29s, 16 unrelated filtered skips); full TypeScript exit 0 | `/workspace/loom-followup-root-review/work/review-advanced-catalog/receipt.md`; `/tmp/loom-advanced-catalog-{focused,tsc}.log` |
| Semantic-inventory race controlled native proof | coordinator red reproduces correlated stale 409 (17.937s); green has zero semantic-inventory requests, five real commands 200, exact four rows, full workspace and reload intact (19.896s). Scoped repair proof, not ordinary whole-case acceptance; initial optional-dispatch diagnostic retained as incomplete | `/tmp/loom-ordinary-semantic-race/{red-coordinator-2,green-coordinator}/`; red `correlated-api.log` |
| Carry-in attempt 1 | source-step Preview/Cancel/Apply/edit/reload/restore passes; complete case fails stale public-field-name Group oracle (exit 1, 54.119s). Parent independently resolves stable IDs to patient_gender/status and verifies exact four tuples/counts in real receipt. Initial adapter review subsequently invalidated by carry-in 2; whole-case repair/rerun pending | `/tmp/loom-followup-carry-in-1/`; `/tmp/loom-carry-group-alias/parent-proof.json` |
| Prior root UI build | actual exit 0; package Vite 46.53s / demo 24.79s; chunk-size warning remains. Later row-panel epoch repair invalidates affected build coverage; final UI build must repeat | `/tmp/loom-followup-final-catalog-build.log` |
| Initial Group preview public-name adapter (invalidated) | earlier 13-test/review result used an incorrect internal sourceColumns mock shape. Native carry-in 2 disproved public compatibility; retain these passes only as historical focused evidence, not adapter acceptance | `/workspace/loom-followup-native-review/work/review-native/group-preview-alias-receipt.md`; `/tmp/loom-group-alias-parent-focused.log` |
| Ordinary attempt 3 | WHOLE PASS, 26.431s, current source cec3182784afcb07ec1926ef3a49757085c2fd9db380322ec7947f4056fb40e4; six parent-verified actions, zero errors/409; API source/binary unchanged | `/tmp/loom-followup-ordinary-3/` |
| Carry-in attempt 2 | WHOLE FAIL, 46.039s; eight source-step actions pass. Group helper reads absent internal candidateConstruction.sourceColumns; actual public proposal uses stages[source_projection].columns. Public-shape repair and carry-in helper/source registration now reviewed; complete native rerun pending | `/tmp/loom-followup-carry-in-2/`; invalidation appended to `/workspace/loom-followup-native-review/work/review-native/group-preview-alias-receipt.md` |
| Public Group helper/registration repair | reviewed public stages-only mapping; no internal sourceColumns fallback. Parent old-helper TypeError reproduced; repaired main resolves unchanged real response to exact four literal tuples/counts. Parent registered carry checks 21/21 pass, actual exit 0 (14.691s); writer 21 pass (1.800s), independent 13 tests plus actual public record/13 adversarial controls pass | `/workspace/loom-followup-candidate/work/review-group-public/receipt.md`; `/tmp/loom-group-public-parent-registered.log`; `/tmp/loom-public-group-proposal/{parent-red.log,parent-green-main.log,parent-green-proof.json}` |
| Carry-in attempt 3 | WHOLE FAIL, 79.633s at c352273281cb3abb527d17b61db18cf084016e9f33b57c0ff07d0dd59517405b. Eight source-step actions and exact Group tuple/count Preview/Cancel/Apply pass; delayed row choices reopen chooser (1 instead of 0), a confirmed product race | `/tmp/loom-followup-carry-in-3/` |
| Row chooser handoff repair | reviewed two-path epoch invalidation for workspace Group/Related handoff; no shared cancellation, new effects or owner teardown. Actual Row Settings 54 tests pass (9.17s), full test TypeScript exit 0. Parent native red proves chooser 0→late actual 200 choices→1 with Group visible, unchanged saved state and zero blocked writes; parent repaired native green actual exit 0: after real response.finished and two frames chooser stays 0, Group visible, saved identity unchanged, blocked writes 0; elapsed not instrumented | `/workspace/loom-followup-root-review/work/review-row-settings-closure/receipt.md`; `/tmp/loom-row-settings-closure-{red,green}.log`; `/tmp/loom-row-choices-race/{red,green}-coordinator.log` |
| Final chooser root UI build | actual exit 0 including TypeScript; package Vite 34.02s / demo 24.95s. No later product UI/source change recorded; chunk warning remains | `/tmp/loom-followup-final-chooser-build.log` |
| Carry-in attempt 4 | WHOLE PASS, 73.707s, 17 native actions: eight source-step Preview/Cancel/Apply/edit/reload/restore actions, exact four-tuple Group Preview/Cancel/Apply with closed chooser, stage-bound ALL contributors, reload exact counts/values, saved Group edit/removal/reload restores original source. Zero browser/write/HTTP conflicts; source/API identity unchanged before/after | `/tmp/loom-followup-carry-in-4/{command.json,native-evidence.json}` |
| Related rows attempt 1 | WHOLE PASS, 56.780s, seven actions; exact six preserve-parent/five drop-parent pairs and full lifecycle at source 45142ac0… | `/tmp/loom-followup-related-rows-1/` |
| Upstream dependency attempt 1 | WHOLE PASS, 51.134s, four actions; atomic dependency rejection plus Group removal/reload at source 45142ac0…. Includes one expected HTTP 422 INVALID_IMPORTED_RELATIONSHIP; do not describe it as zero HTTP errors globally | `/tmp/loom-followup-upstream-dependency-1/` |
| Mixed attempt 1 | WHOLE FAIL, 106.045s; Observation FIRST→ALL edit/reload and exact six-pair expansion retaining ALL lists pass. Explicit per-match scalar Preview returns 503 BACKEND_UNAVAILABLE; no Apply. RELATED_FIELD drops hidden __loom_dynamic_runtime_keys; validator reports missing identifier_by_system metadata | `/tmp/loom-followup-mixed-1/`; `/tmp/loom-mixed-scalar-metadata/parent-red.json` |
| Mixed metadata repair (investigation checkpoint) | subsequently completed as compiler 21 below. Original minimal preserveDynamicRuntimeKeys admission and default-chain regression preserve generic historical 20 read/nonexecution/cache separation and current-only proposal-binding guards | candidate-contract isolated repair; parent persisted receipt_58426f50… reproduction is 503 with full workspace/version/digest unchanged |
| Compiler-21 related inventory repair | integrated, reviewed seven-path unit: only RELATED_FIELD/RELATED_SOURCE admitted to existing hidden metadata carry; no metadata invention, drift/type guards retained, private keys removed before public output. Full affected compiler/.../execution/explorer/authoringv2/recipeimport/lifecycle pass with actual exit 0 (execution 11.310s; lifecycle 22.153s). Six real-Arango RF/RS known/drift/type cases pass (1.51s); parent independently passes all six (1.18s) | `/workspace/loom-followup-candidate-review/work/review-candidate/inventory21/receipt.md`; `/tmp/loom-related-inventory-parent-green.log` |
| Fresh compiler-21 scalar native proof | no-Apply native proof passes (20.079s); parent fresh public request replay 200/READY, exact six pair/list/scalar literals, all prior public values preserved, no private leak. Cancel preserves full workspace/version/digest | `/tmp/loom-scalar-api21-proof/fresh-worker/result.json`; `/tmp/loom-scalar-api21-proof/parent-green.json` |
| Previous proposal refusal | old compiler-20 proposal returns 503 RECEIPT_STORE_UNAVAILABLE from current-contract guard. Initial private expected-409 assumption was wrong and stopped before fresh browser proof; not a clean 409 migration or universal old-proposal readability claim | `/tmp/loom-scalar-api21-proof/worker/old-v20-receipt.json` |
| Group missing-policy extension | five reviewed harness/fixture paths add explicit EXCLUDE Preview/Cancel/Apply/reload then restore GROUP, preserving all prior assertions and deadlines; parent 22 registered checks pass (actual exit 0, 0.993s). Full 23-action carry-in rerun pending | `/workspace/loom-followup-root-review/work/review-group-missing-policy/receipt.md`; `/tmp/loom-group-missing-policy-parent-checks.log` |
| Refreshed authenticated doctor | actual exit 2 (0.309s): `usage: set LOOM_E2E_TOKEN or pass --token-file PATH`; no token available and no authenticated remote check performed. This credential blocker is separate from owned native coverage | `/tmp/loom-followup-authenticated-doctor-final.log`; earlier readiness `/tmp/loom-followup-readiness-1794553181550017402/` |

The successful draft source fingerprint is
`538fe62f7a3537eaa1debb9a32ee5a7c9f6856dd12cbfd0da6911a5dfb7fa7e3`.
Its framework baseline metadata is not the working-branch baseline. The later reducer delta is covered by 27 focused tests and native red/green diagnosis; unchanged UI coverage is reused rather than rerunning the broad 210-test suite solely for that one-line repair. The prior root UI production build passed; the later row-panel repair now has a repeated passing final build; complete native acceptance remains incomplete. Backend ordering review is closed; source-label and row-focus deltas are covered by 40 focused tests and full test TypeScript, not a claimed full current 210-test rerun. Earlier draft
attempts remain recorded: 30s timeout without a report; 171 pass/29 fail; then
199 pass/1 fail. They are failed/incomplete investigations, not additional passes.
The isolated source-collection adapter experiment was abandoned and is excluded.
The existing **Rows per starting record** control remains the single source-row
entry point and retains its supported choices and empty policies.

Exact-source inherited same-model reviews cover backend candidates/receipt
isolation, shared owner/workspace, row/traversal projections, ordinary policy,
root replacement, single source-row entry, focus restoration, and the native
verifier. Counts overlap and must not be summed. No cross-model review is claimed.
Raw scoped receipts are under `/workspace/loom-followup-{candidate-review,workspace-review,projection-review,root-review,source-collection-review,native-review}/work/`.
Demo-only layout manifest: `b0f5dc3540a2339d9eb24c383275624c5766abe94930641c94492038d5efed22`.
Later narrow manifests: inspector `6ba6b3c866f1cf347b30429b0a181a230d22aecd6639611f7fa1735360060b19`;
saved preview `1b93a0d9a6cf86b3af025d52ba223b8ee4849f60c99d9eddb933de61a6397fa5`;
resolver `ea7c38cf55826a8e2cb2199628e595286942bf34ea7c3905ea506c9ccc2c1b42`;
ordering copy `52c5108a2a1b9d29fc9a923176786b46b162044da1b16a96bb11940ce9ef9063`
(component SHA `87fe9f2e9fba42c8b61c2c2ec2c03c4f4c2221e1854ba03311f5972773963258`).
Key manifests: root `80c3a7185cfcec169a7be2455093cd587f9a498182e3f60a45985eaa6d244a37`;
ordinary policy `72cd05450ef3266ce9f6f03f5c3d5bde10f70efb16a63152f89beb23eb0ffd64`;
single entry `ad6e907b54b1cdea11cecc0bf788c4d57451ea31f034e8b0328126e960c25194`;
current focus context `e4ea5072182fe46ff7a05212f23accfefc5c7bdf8500aa5e252f6de488002c2f`.
Focus review passes 19 focused tests plus ten actual-helper controls; its unrelated
later source-column dispatch change is outside that receipt. Relevant changes
invalidate affected coverage. Verifier review/discovery is not native acceptance.

Before the failed native attempt, main/runtime equality covered 2,066 files
(`/tmp/loom-runtime-final-sync-1794554428436815185/source-manifest.json`). Owned
readiness and identity passed under `/tmp/loom-followup-final-readiness-1794554463214956229/`.
That attempt's 1,589-file source fingerprint was
`4a4fa9508211f25ab1fbd8f74755f1d9375756fa3cec8f1dc7499096058c741d`,
API source `ae1646e66a6190b5a7cc3994e3056be2b1573d4ac460c3177ad1687bde58de80`,
binary `04294a9de2b2d703ead1230243207255797a0d0a20692ce3227bd5af013e3d32`.
Readiness is not product acceptance. The measured 32-column notation comparison
does not prove the actual 267-column Calypr layout or a native lifecycle.

The imported pass belongs to native fingerprint
`00990f30edd660c72d6cfa6c251e0033ddda4a544527768a866b8f54eb7b2ae1`.
The later first-table scheduling gate changes current source to `557e277f75787a93245e18d7ee0a285c61e782b80e48ac2660290b0c660033a7`;
retain its full identity in each new native report rather than promoting earlier passes.
Imported and ordinary now have whole passes at their individually recorded fingerprints;
ordinary attempt 2 remains failed historical evidence. The earlier graph/row whole passes and canonical comparison
at `01b7fd280661390fd82e0fa776af21f3b3967b675e31f99760bd104bc24b328f`
retain their exact scopes; final graph/row reruns remain required after the harness
batch. Current API source is
`e64d6819523a3172103f3f58a93816bacdba9257de973d0dd27e1381758152e8`,
binary `3b1eb3335b424fb5b8a49876ccdafdef7327588cd983c0e7502398e6b6536241`.
The earlier API identities above are historical attempt identities, not current.
Ordering review manifest is
`75b465357f4df552de900676d9ec87b48d7bcb9220b4617a2c90e205747b6cb4`;
readability `c0be3bc910ecb3c0144c157d0cf4f0b72e5024df28c3510dbc32e92e06ad4520`;
row focus `4766f94e947f78a9be0bc75596fc6a4b9c7dcdf53998f0f4219cec7eca6df5c6`;
catalog equivalence `55a94ded2685d364d3c9f51a1af2c775e071c84835884c3daf6fa6a9a82b958f`;
legacy selectors `bedee3e531cd8f9a9a623da228d0f276013cf69d208f7be8555d18aeaab9f9ad`.
First-table gate manifest `90b6039ff0915c8e1f40503372b84c94952ef5db02be13a98d66705aa7c941e0`
binds Builder `1fb70f984c15a06448e29a3fdec691fa80de7baae2d16b421d41ffd8e3f192d5`
and helper regression `ccfe2bd4262c2d9f21faada6112c4458edf9be0d6f278e06b76bf8a162b1f908`.
Owned qualitative screenshot review found the five editing views usable, with small
graph text and internal scrolling at 900px; it does not establish the actual
267-column authenticated Calypr frame. Fresh 100-operation fixture creation and
its native case remain pending; no absent historical fixture proof is substituted.

Backend contracts and contributor-ordering implementation are now committed in
`60af4516`; this does not close UI/native acceptance. Advanced catalog review
manifest is `d8f3de742f6f426d9ce8b8ef81eaef5666b82025b8ea8a8da5e69c89b4d27267`; exact production/test hashes are retained in
`/workspace/loom-followup-root-review/work/review-advanced-catalog/manifest.json`
(Builder `8c9712b8…`, regression `4302b3e1…`). That UI build completed with actual exit 0; the subsequent row-panel repair requires
a repeated final build, as recorded above.

The historical, now public-shape-invalidated Group adapter manifest is
`9c191bab919d8fb8db87d63dc07dc7d78c2166cb77239f59289aa322405eb9a3`.
Ordinary attempt 3 now has the current-source whole pass recorded above. Carry-in
attempt 2 invalidates the initial Group adapter public-shape review: the same-model
review missed a mocked internal shape, and carry-in registration omitted the helper
focused tests/source file. The independent four-tuple real-receipt diagnostic remains
valid but proves no complete Group lifecycle. The repaired helper/registry identity is now recorded below;
API source and binary are unchanged.

The final public Group helper/test/registry manifest is
`bd0f64ed2e094aafbdb28ef37c85455e695f8cb56f0e9fd311995e3a22c83209`.
Its three paths are helper `505a4a3e…`, test `0fee74ca…`, registry `255a2733…`;
full hashes are in the review manifest. The unmodified public request/proposal/preview
record at `/tmp/loom-public-group-proposal/worker/actual-public.json` was captured
without Apply; native Cancel left saved state unchanged and blocked writes were zero.
An initial private validator confused numeric blockedWrites=0 with an array and is
retained as a diagnostic error, not a product/helper failure. No literal expectations,
oracle ordering, native controls or deadlines were relaxed. Carry-in now registers
the existing complementary helper tests and its source identity.

Carry-in attempt 3 failed against native fingerprint
`c352273281cb3abb527d17b61db18cf084016e9f33b57c0ff07d0dd59517405b`;
its exact partial passes and later product race are recorded above. API source/binary
remain unchanged. Product row-panel repair now has a repeated passing final UI build. Earlier invalid review
and native failures remain retained; this repair review does not establish full
Group lifecycle or authenticated Calypr acceptance.

Row chooser repair manifest:
`a6714cb76d3a9e758ed4635dbfc722f5d4593e41e0fd1d8ead119b2fef5b3327`;
production Row Settings `7990d0cb…`, regression `d45d3d20…` (full hashes in receipt
manifest). Pending choices success/error responses are invalidated only at workspace
handoff. Parent repaired native green now passes (no elapsed measurement). Repeated final
root UI build now passes, and carry-in attempt 4 completes its whole lifecycle.
The passing carry-in/related/upstream source is
`45142ac0e16c1a5fdf2c066bb0e526e512e076cd5b8dd9bd04b8b4ce57ebb3e1`;
API source/binary remain unchanged. Related rows and upstream dependency now pass at that source; mixed attempt 1 exposes
a new confirmed backend metadata failure.
Final imported/ordinary/graph-first/row-first reruns and strict canonical comparison
remain pending after the stable remainder; fresh 100-operation history preparation
is last. Remaining full matrix and authenticated Calypr acceptance are incomplete.

Compiler-21 repair/review and focused/full affected checks are complete; the new
whole mixed case is running with no result yet. All final compiler-21 native matrix
cases remain pending. Earlier 45142ac0 passes retain compiler-20 snapshot scope.
The final UI build remains applicable because no UI source changed.

Compiler-21 unit manifest:
`6db91363bdae7fb0e886ca24b075f7d37d54a72167e6c6f2d9cb9514253a4ab8`.
Generic v20 receipts remain inspectable with required shape digest, cannot execute,
and have version-separated cache identity. Historical proposal bindings still require
current contract; their readability is not promised. Prior RF 503 and old-binary RS
missing-inventory red remain retained. This reviewed backend unit is ready for a
coherent local commit with its tests and scoped verification, without closing UI
or package acceptance.

Current API readiness doctor passes (2.747s), with source
`8be4c30004b88ea26d3c6d50f5b7735abe65c3340ec5b5efa6510b09cfe7e9e5`,
binary `94ff59cafbaa78ed8bcdb9916629ad409f3b9c4e4888ea37c1b7ef055be3a07e`.
Current 2,069-file main/runtime equality yields native fingerprint
`8d23bdb2ad45a99c526ddfc7553cbb1b38af4d78ba0eb2559a7c88bc9601d62e`.
The older API identities above belong to their respective attempts. Group EXCLUDE
extension review manifest is
`da90249ab6e6352c78b7148ea2eaaccd8e4d0712e8640e25f97ae7a1d4e4bf45`;
its whole 23-action carry-in run and other final cases remain pending. Native
no-Apply scalar proof is scoped repair evidence, not a substitute for mixed Apply,
edit/removal/reload or authenticated Calypr acceptance.

The current 12-case matrix and pending criteria are in
[fixtures/ACCEPTANCE.md](fixtures/ACCEPTANCE.md). The remaining current-source native
lifecycles and actual Calypr frame checks remain incomplete. To unblock authenticated
readiness, provide a short-lived token through a secure 0600 file, then run:

```bash
node .codex/skills/verify-loom-ui/scripts/verify.mjs doctor --token-file /secure/loom-e2e.token
node .codex/skills/verify-loom-ui/scripts/verify.mjs drive --token-file /secure/loom-e2e.token --chrome /usr/bin/chromium
```

Drive establishes load only; complete authoring lifecycle and desktop captures
remain necessary. No credentials were invented, no acceptance waived, and no
production deployment/publication performed.

## Original 01–07 evidence (historical)

## Original scope and source

The implementation starts at `1685bc4c2adda944e380e721a80fcdcfbf04def5`
on `feature/default-recipe-authoring`, containing
`4b64797e3dcf391114471e86555ad511bb084b0d`. Both were confirmed on the
requested remote branch before implementation. No alternate baseline was used.

The default recipe remains byte-for-byte unchanged: SHA-256
`998c6aead0d3f432f3f0cf4d3fb515cbbacda9af53c0c090f457c3931b709dad`.
Go and UI authoring semantics are version **12**; accepted version 11 documents
retain the explicit legacy adapter until a current command migrates them.
Compiler contract **19** uses execution fingerprint algorithm **3**, including
source dynamic scalar/list cardinality. Historical ordinary receipts remain
readable but require explicit recompilation before execution; their compilation
cache keys differ. Historical proposal receipts cannot authorize an Apply.
The immutable recipe pin and authored draft are preserved during recompilation.

Ordinary documents own `Route` and `Rows`. Imported documents own the immutable
recipe pin plus the typed project-local source overlay. The authoring network
is derived, never a second persisted canvas. Project additions have stable
`project_` identities and exact authorized catalog edges. Source-step,
occurrence, compiled column and construction anchor bindings are explicit.
Imported graph commands use receipt-backed source proposals; ordinary graph
commands retain `ADD_ROUTE` and `REMOVE_ROUTE`.

Optional related values preserve rows. Required matching filters the applicable
path; it does not expand rows. Source item expansion, per-input related-record
pairs, named source groups and construction Group key tuples remain distinct.
Independent related expansions that would require an unspecified product are
rejected. Imported preserve-mode root edits remain unavailable; deliberate
replacement discloses pin detachment and reset.

The original 01–07 frozen API source identity was
`8d6b1c244537b92e1eaafa50b4da24c1952310186f3c18c4361d907f29197a27`;
its binary SHA-256 is
`c956bddd7a4029e8a91b7939f6f2b621fb88204c7c3574846799ade65eefff28`.
The Group runtime repair is complete and reviewed. Final integrated source and
commit identity remain pending while native acceptance is incomplete.

## Automated evidence

Raw development evidence is retained locally under `work/graph-rows/` and
`work/graph-rows-runtime/`; these disposable logs are intentionally untracked.

| Check | Result | Evidence |
| --- | --- | --- |
| Full authoringv2, recipeimport, lifecycle suites | pass | `backend-frozen.log` (0.323s / 0.105s / 26.952s) |
| Final full IR, compiler/lower, AQL renderer and execution suites | pass | `group-runtime-packages.log` (0.018s / 8.331s / 0.072s / 11.800s) |
| Public HTTP proposals, strict intents and OpenAPI ownership | pass | `server-final.log`, `openapi-ownership.log` |
| Final registered draft behavior | 193 passed, zero skipped | `behavior-drafts-complete.log`; `/tmp/loom-graph-rows-drafts-complete` (46.50s) |
| Final registered real-Arango graph fixture | ten original cases plus seven dynamic-source Group cases pass | `api-complete.log`; `/tmp/loom-graph-rows-api-complete` (8.66s including build) |
| Production fixture ingest validation | pass | `fixture-ingest-final.log` (5.310s) |
| Final UI production build | pass | `ui-build-complete.log` (Vite 33.41s plus production TypeScript/assets) |
| Final UI typecheck | pass | `typecheck-complete.log` |
| Final server receipt/source/network checks | pass | `server-complete.log` (6.416s) |
| Compiler-contract migration | pass | `receipt-version19.log` (0.079s), old execution refused and cache key changes |
| Group receipt replay and metadata integrity | pass | `group-receipt-green.log` (9.661s); red logs retain original provenance and scalar/list failures |
| Compiler value-policy normalization | six mappings plus default provenance pass | `value-policy-fixed.log` |

The focused package commands are `go test ./internal/explorer/authoringv2
./internal/explorer/recipeimport ./internal/explorer/lifecycle` and `go test
./internal/dataframe/compiler/ir ./internal/dataframe/compiler/lower
./internal/dataframe/compiler/render/aql ./internal/dataframe/execution`. The UI
build is the `@calypr/loom-ui` package `npm run build`. Registered behavior/API
runs retain their exact invocation and coverage in the cited evidence directories.

The Arango cases assert literal expected identities and values for zero/one/many
matches, two-by-three independent branches without a product, distinct Patient
paths, required-path filtering, shared input/related-record pairs, item expansion
and empty items, named empty groups, Group tuples, summaries and retained values.
Additional Group cases use the real engine and Arango to prove keyed, keyless,
empty and repeated grouping while retaining every contributing dynamic-key
inventory. Earlier unknown keys remain rejected; hidden validation metadata never
leaks into public results. The coordinator reproduced seven failures against the
old implementation before repair (`group-runtime-red.log`, 0.141s).
The oracle was authored independently of the compiler and rejects arbitrary first
values, missing pairs, reordered values and accidental Cartesian rows.

## Native evidence

All cases run through the registered `graph-rows` scenario and official
Playwright runner, in an isolated frozen checkout and the owned local
`loom-dev-cloud` stack. No production stack is changed. Each run records all
source hashes, the aggregate fingerprint, API source/binary identity, runtime
configuration, recipe hash, actions and sanitized request diagnostics before and
after. Invoke `node scripts/run-graph-rows-native.mjs --case <case>` with the
owned-target configuration in [ACCEPTANCE.md](fixtures/ACCEPTANCE.md). Raw logs
are `work/graph-rows-runtime/native-<case>-<attempt>.log`. A source or binary change invalidates the run. Native action/request
budgets originally enforced five seconds, with zero retries. The operator
explicitly removed the five-second timing gate on 2026-11-13: final functional
acceptance uses bounded 30s actions/requests, with all exact value, pin, provenance,
Cancel, persistence and CAS checks retained. Measured latency is reported separately
and these passes make no five-second performance claim. A separate baseline-envelope
setup probe has an explicit 30s deadline. The 100-operation fixture is prepared
through real sequential proposals, receipt previews and CAS Applies under a
900s setup deadline with 30s setup requests. Native history controls retain
the 180s test deadline and use the same 30s functional action/request bounds.
Final reruns use 30s expect/action/navigation/API bounds and zero retries;
surrounding process deadlines are 240s, or 420s for loaded history including
a separate 120s current-source admission. All 100 intermediate
calculated results and all 100 final values are checked; this does not claim
100 native operation-creation lifecycles.

| Case | Result | Raw evidence |
| --- | --- | --- |
| `imported` | pass: all five outputs; relationship Preview/Cancel/Apply/edit/removal/reload; real stale draft; layout and keyboard | `/tmp/loom-graph-rows-imported-8/results.json` (73.176s test, zero retries) |
| `ordinary` | pass: ordinary route add, required match, removal and reload | `/tmp/loom-graph-rows-ordinary-2/results.json` (21.075s test, zero retries) |
| `carry-in` | incomplete: source-step and Group lifecycle checks exist; latest whole case fails five-second Group Apply budget (5.29s, command 4.52s) | `/tmp/loom-graph-rows-carry-in-12/results.json`; earlier 17-action prefix is partial evidence only |
| `related-rows` | incomplete on current snapshot: initial ResearchSubject tab click exceeds five seconds; earlier snapshot passed exact pairs/lifecycle | `/tmp/loom-graph-rows-related-rows-3/results.json` |
| `upstream-dependency` | incomplete: five-second initial Builder API GET timeout before Group/source actions | `/tmp/loom-graph-rows-upstream-dependency-1/results.json` |
| `history-stress` | 100-operation production fixture created and checked; current-source admission/native acceptance running (attempt 5) | `/tmp/loom-graph-rows-history-stress-4/` (creation 830.492s); prior native timeout is not a pass |

First failures are retained, including the initial readiness timeouts, an incorrect
verifier join by authored name instead of stable column ID, and the Group receipt
mismatch. Failed runs are not counted as acceptance. The ID join was corrected
against the production binding, and runtime CPU configuration was measured and
recorded. The later operator timing override permits bounded functional reruns;
the original five-second failures remain raw evidence.

The [compact layout comparison](fixtures/LAYOUT-COMPARISON.md) uses the same real
default recipe, catalog, source steps and four preview rows. At 1536×900 the
retained beside arrangement shows four rows in a 330px preview; a bounded
stacked candidate leaves a 122px preview with one fully visible row. Both were
also measured at 1536×1100 and restored to 900px. This comparison is presentation
evidence, separate from native lifecycle acceptance.

## Exact-snapshot review

Two independent reviewers used the inherited model; this is **same-model review**,
not cross-model review. The coordinator reproduced retained findings before repair.
Backend review covered pin/overlay ownership, exact provenance, catalog identity,
strict wire intents, receipt binding and CAS. UI review covered proposal ownership,
source-step review, effective match policy, one graph inspector and popup hit testing.

Retained findings fixed and rechecked: missing default catalog provenance; public
VALUE policy conflicting with the overlay contract; ambiguous legacy route identity;
wrong exact parent node acceptance; clipped compact row controls; and pinned rather
than effective source match text. Later value-policy display normalization and
ordinary removal confirmation counts received scoped reviews with no new verified
findings. Compiler-contract 19 also received a scoped migration review: old v18
and v15 receipts return `RECEIPT_RECOMPILE_REQUIRED` before validation/execution;
new cache keys differ and current-only proposal authorization remains intact.
Receipt: `/workspace/loom-review-a/work/review/compiler19-review.txt`.

Review receipts remain in `/workspace/loom-review-a/work/review/` and
`/workspace/loom-review-b/work/review-ui/`. The repaired backend manifest is
`959f791c18e48b3df68c54a58a8656f0baac0edf87f2ab95d849bd7b1bcd80e7`;
its value-policy delta manifest is
`f8af617c89d8f2b0b85de155e79fd5b412e7f26a8713710c473de266aad90c20`.
The final UI base manifest is
`39f2b0b055c562c4c616b205550ec03f30ffb1b23c4b0bb07450d0ebe6a693a6`;
`tiny-final-manifest.json` identifies the later two-file reviewed display delta.
The final Group repair was reviewed on helper hash
`a8038dbcbc1b135179f75125a2c51debcdb75740e93108298c6fb2f75442fb5b`
and regression hash
`712adb758356c9e63197e3b93158920c35aca1bb8b812d3d74e3bd32654ef382`.
The coordinator reproduced the reviewer finding that source-only dynamic `Many`
cardinality was not fingerprinted, then repaired it. Real Group replay and seven
independent metadata-tamper controls pass. Receipt:
`/workspace/loom-review-a/work/review/group-receipt-review.txt`.
Compiler-version migration hashes are
`9bbbc0af20756bc1c20fd85d769d6a591e4418eca3e95f63cc82f40cea952f53`
and `ebcd92ed27fe8c9fd9bf333d9c33e931ddf31fc30ffa9e1cb0bbf68ad08957f0`.

Independent acceptance-verifier review found no verified findings. Its final
25-file manifest SHA-256 is
`d2f8076481632da5eaf738af0d7722d72e9ca512371916aeea6be8cd28606af2`;
receipt: `/workspace/loom-acceptance-review/work/review-acceptance/final-budget-review.md`.
Earlier nine focused oracle/integrity/history checks passed; later admission
checks pass 15 tests, and official discovery now finds six cases in one file. Review found that Playwright action timeout did not
constrain direct API requests; the coordinator reproduced a 5200ms response being
accepted in 5262ms. Native API calls originally explicitly used 5000ms, and the delayed
response control rejected the response at 5011ms. Under the later operator
override the final functional runs use 30000ms; this changes the timing gate,
not the value or integrity assertions. These focused checks do not substitute
for native execution.

The final Group runtime repair has a separate nine-file reviewed manifest:
`98b528f76f4d43dcf7641fcc0d0f155d9501c324d4721fbdb4c748d8f6b81f8c`.
It extends the existing typed stage IR and renderer to carry each contributing
inventory through grouping, then recursively checks every inventory at execution.
No family is selected arbitrarily and no validation is disabled. Independent IR
variable/projection tamper controls and strict mixed-family checks pass. Receipt:
`/workspace/loom-review-a/work/review/group-runtime-review.txt`. This scoped delta
also covers the added Arango fixture helper beyond the earlier verifier manifest.

Group contributor UI now offers existing public scalar input columns even when
complex imported sources cannot expose unprojected raw FHIR field choices. It
authors the exact `InputColumnID` with explicit `ALL`, excludes identity/list/
object values, and keeps keys and summaries intact. Same-model review caught a
stale coded-capability response that could retarget a Group; the coordinator
reproduced it and the final guard blocks emission until the saved input is
available. Positive derived-stage edits preserve the original anchor. The focused
reshape suite passes 108 tests, five new checks pass in the integrated tree, and
production and focused test TypeScript checks pass. Receipt:
`/workspace/loom-review-b/work/review-ui/input-contributors-final-review.md`.

The receipt store removes repeated content hashing only at already validated
boundaries, retaining caller-body revalidation after storage I/O and all legacy
checks. Actual modern/legacy corruption, concurrent writer, cancellation,
idempotent metadata and aliased-body mutation tests pass. Same-model review
manifest: `a50db8d670dff29b444a40b10757b4936724646aa66e3ddcc44b2557be25c549`;
receipt: `/workspace/loom-review-a/work/review/receipt-store-final-review.md`.
On the identical actual 2,568,639-byte 100-step artifact, one-CPU store-path
samples were 1.546/1.391/1.298s before and 0.985/0.990/1.163s after: a 29% median
reduction with unchanged receipt ID. This is not a native end-to-end speed claim.
Parent regression evidence: `receipt-store-final.log` (0.091s).

The loaded-history admission review covers original-proof integrity, current owned
target/generation, unchanged draft 103, all 100 operation IDs, inputs and schemas,
and all 300 calculated cells. Current reconciliation and preview record fresh
source/API identity before and after. The original creation artifact SHA-256 is
`1c691746b7375313751511437c9f14884cbc021d7541e3c09407c19cca60bffc`.
The private original includes development session metadata and must not be shared
or committed; normal and reuse summaries omit secrets and use exclusive mode-0600
writes. Filesystem alias/race controls preserve original bytes. Receipt:
`/workspace/loom-acceptance-review/work/review-acceptance/history-admission-final-review.md`;
eight-file manifest `1ecece5469364a7b80ca39e2481c7a4232f40c4a3b27599522ecfcfe3f691070`.

The separate upstream-dependency case preserves the complete carry-in lifecycle
and independently checks exact Group input binding, literal results and rejected
upstream removal without saved mutation. Its five-path review manifest is
`301a2b32e8dde5813a2e6945b974c87db60680dea24573e87c30ebf5ba06a406`;
receipt: `/workspace/loom-acceptance-review/work/review-acceptance/upstream-extraction-final-review.md`.
Both are same-model reviews with no unresolved verified findings. An extracted
successful prefix of a failed run never counts as a passing whole case.

The final operator-authorized functional-bounds delta received scoped same-model
review with no verified findings. Only the 30s observation bounds, elapsed-time
and exceeds-five-seconds telemetry, and related comments changed; functional
assertions, data, source/API provenance, immutable pin checks, CAS, zero retries
and the 180s native test deadline remain intact. The 15 focused controls pass
with zero skips (8.968s), and official discovery finds six tests. Receipt and
three-path manifest:
`/workspace/loom-acceptance-review/work/review-acceptance/user-functional-bounds/{receipt.md,manifest.json}`;
manifest SHA-256
`d9e741dfc2ea472fa801df1f254c825fe1a35bcf00d11982959ff28a088066c1`.
Original failed raw runs remain unchanged.

## External limitation

Authenticated Calypr acceptance is blocked. The current skill doctor exits **2**:
`usage: set LOOM_E2E_TOKEN or pass --token-file PATH`.
Evidence: `work/graph-rows/authenticated-doctor-closeout.log`. No short-lived
credential is configured. The authenticated doctor, browser drive and integrated
authoring lifecycle therefore remain unrun. A local fixture pass cannot replace
these checks, and the current authenticated drive alone proves load rather than
the complete authoring lifecycle. The operator subsequently authorized pushing the reviewed code with the
remaining verification gaps documented; deployment and publication remain out
of scope.

## Pushed snapshot and unfinished verification

The operator explicitly requested pushing the reviewed implementation while the
remaining functional verification stays open. The final 30s carry-in attempt
completed its focused checks and official test discovery, then the execution
connection failed; no completed native result was captured. The original
five-second failures remain recorded and are not relabeled as passes. Tickets
remain `verification-in-progress`; this push does not close acceptance.

The final implementation/fixture manifest covers **113 explicit paths**
(documentation excluded), represented as sorted compact JSON of path to SHA-256.
Its SHA-256 is `930c8f3db1f560ee5c3ef8627b79068edb940c2f4fc52edb50a4b49bcde5eec2`.
Raw local manifest: `work/graph-rows/final-source-manifest.json`.
