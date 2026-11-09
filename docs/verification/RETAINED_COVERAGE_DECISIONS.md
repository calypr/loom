# Retained checks and deleted coverage decisions

This is a scoped source assessment, not proof that all 86 deleted Playwright specs
have equivalent replacements. Runtime results live outside Git. Use
`node scripts/verify-builder-formal-core.mjs --phase inventory` for current routes.

## Remaining native checks

| Retained test under `scripts/verify-ui/helpers/tests/` | Decision and actual boundary |
| --- | --- |
| `basic-named-cohort-presence-oracle.test.mjs` | Keep literal membership/Boolean/oracle checks; remove deleted-workflow source regex checks. Browser-origin capture and saved-table reload wiring remain unverified. |
| `created-explorer-scope.test.mjs` | Keep actual canonical routes, server-assigned IDs, and invalid-scope rejection. No claim that the deleted population browser workflow uses those routes. |
| `post-pivot-related-source-oracle.test.mjs` | Keep witness, multiplicity, source scope, identity, edit/restore and retained workflow checks; remove deleted spec registration/source assertions. |
| `append-reload-timing.test.mjs` | Keep exact output/header/row multiset and timing helper behavior. Replace Chromium over test-built HTML with bounded callback inputs. This does not prove browser rendering, native APPEND action ordering, or real reload. |
| `fixture-context.test.mjs` | Check the retained producer's full project/generation-bound oracle and reject foreign bindings. Do not restore the deleted journey runner merely to exercise its report validator. |
| `policy-self-test-structured-failure.test.mjs` | Migrate fail-closed outcome checks to the active formal verdict/tool contract. Deleted Policy receipt builders and TraceProgress source slicing are not current product contracts. |
| `cda-current-draft-upstream-append-contract.test.mjs` | Retire this obsolete harness-only test: its sole assertion checked deleted spec/report-builder source spelling and a constant count of 26 report requirements. It never executed APPEND. The existing current-draft upstream APPEND oracle test remains connected; actual APPEND row/schema/reload behavior is still a separate requirement. |

The core dev CLI again loads without optional browser helpers. Retired browser
journey commands reject explicitly. Native test selection follows executable
imports rather than quoted browser words, includes skill-owned tests, and records
all exclusions instead of silently treating them as covered.

## Valuable deleted guarantees to preserve

| Deleted source (from HEAD) | Current protection / missing boundary | Decision |
| --- | --- | --- |
| `builder-saved-route-edit.spec.mjs` and `saved-related-expand-route-edit.mjs` | Component/client checks cover Cancel, reviewed Apply/CAS, proposal identity and aggregate history. Public restore checks exact fresh-read workspace identity/content. Complete route-A/B Patient/Observation row semantics through UI reload remain unproven. | Move row/oracle permutations to fast service/API tests; retain one thin Cancel/Apply/reload browser checkpoint. Do not restore the 300-second discovery/report workflow. |
| `verify-cda-quantity-pivot-native-drag-browser.mjs` | Synthetic reorder callbacks cannot prove native geometry/hit targets or persisted row identity. | Worth a small native drag/drop browser case; keep order permutations in fast tests. |
| `cda-group-pivot-published.spec.mjs` and its workflow | UI readiness, backend publication/query and Viewer rendering have separate tests. The assembled revision/receipt/output/exact-row agreement across publish and reload is not established. | Worth one small-fixture publication/Viewer checkpoint, backed by fast publication/query integration. |
| `viewer-row-file-download.spec.mjs` | Unit rendering does not prove the browser download trigger, exact filename or bytes after reload. The old workflow itself used a controlled file response, not a real download backend. | Worth one thin browser download check with a test-owned response; keep backend authorization/identity tests separate. |
| `viewer-output-filtered-dataset-zip.spec.mjs` | Export service tests cover scope and identity; they do not prove the filtered Viewer/modal requests the complete authorized population. | Prefer a mounted Viewer/modal and API check first; reuse a generic browser download checkpoint where sufficient. |

Do not resurrect removed report/catalog/discovery machinery solely to satisfy
source-inspection tests. Preserve independent expected values and user-visible
promises, choose the cheapest valid boundary, and label any unexecuted browser or
durable-storage boundary as unverified. The initial connection pass restored no browser journeys. The subsequent refinement restores the native component boundaries below; historical reports are not current-source evidence.

## Executed restoration and refinement

- Native Chromium now drives production PreviewTable drag/drop and production Viewer FileCell downloads, including configured query/fragment URLs and component reload. It verifies stable row identity, exact paths/filename/bytes/SHA and cleanup in an isolated fixture. Caller/sessionStorage and attachment responses remain test-owned; full Builder/deployed storage is not implied.
- Actual Arango public API/compiler/storage tests now execute exact route-A/B/restored-A rows, CAS/digests, history and fresh repository read-back. The fast structural profile remains available separately.
- Production HTTP publication tests now check compile receipt, persisted revision/output/public types and fresh Viewer identity; materialization is test-owned, so complete published query rows across browser reload remain a separate checkpoint.
- Mounted Viewer and actual API-client tests now check visible facet subsets versus complete-population exports, Cancel/reopen, stale output completion, all 16 response identity mismatch combinations and four canonical aliases. The artifact identity bug found by these tests is fixed.
- Real Arango reactivation exposed two product defects: exclusion diagnostics coalesced missing/null presence after a Preview optimization, and cohort lookup could choose an excluded duplicate logical-ID resource. Both retain the original independent integration assertions and fault controls. Stale canonical-project/trace-identity fixture repairs are maintenance, not product defects.
- Remaining full-CDA read-only profiling is explicitly opt-in and requires its real dataset/index; a fresh fixture database cannot prove that external workload. Bounded local index/row/immutability checks run independently.


## Further recovered boundaries

The native `route` checkpoint now connects production RelatedExpandEditor, lifecycle hook, proposal panel and PreviewTable with literal route A/B/restored-A rows, Cancel/no-write, Apply versions, reload and obsolete response ordering. Its API, Apply and sessionStorage caller are explicitly controlled; it does not close the full BuilderWorkspace/deployed backend checkpoint.

The `publication-rows` API checkpoint now joins the actual compiler/AQL stream, production ClickHouse materializer, Arango publication registry and Explorer activation, fresh Viewer/database reads and literal scoped row oracle. It also checks repeat-publication identity and frozen published rows after later draft/source edits. This closes the previously missing stored-row boundary for that bounded fixture; browser publication/rendering, external authorization, process restart and broader data families remain separate.

The nine unresolved historical Builder worklist records still require their named CDA witnesses, runtime dataset identity, external authorization or deferred performance evidence. These bounded replacements do not promote their old statuses to closed. Retired runner commands in the historical queue are evidence references; use the current project skill for executable routes.

The [deleted-guarantee record](DELETED_GUARANTEES.md) preserves authored model promises and immutable Git recovery points. ConstructionChain now restores a bounded predecessor-reference and predecessor-column subset through the actual Go validator. It does not close the full deleted BuilderOperations scope.


The retained operation-history bridge now replays two exact historical witnesses through actual editor capture and public Go proposal/Apply/Preview. Its applied MEAN witness also checks a fresh public Builder/capabilities read against independently stored workspace bytes and two mounted editor remounts. This recovers saved Group/Filter identity and operation restoration under a controlled repository/catalog. The fixed profile described below extends this to eleven selected historical schedules. Full journey models, durable database reload and browser acceptance remain outside scope. Source faults independently corrupt the public read and editor initialization and must fail the literal saved-operation assertion.


A second retained boundary uses the actual Builder query hook/cache and mounted Workspace: recovery must force a fresh GET, admit its new snapshot, and bind the next saved command to that snapshot and exact CAS identity. Disabling reload forwarding fails the observed request assertion. This recovers one transport-ordering guarantee without restoring the deleted TemporalFilterJourney machinery. Server responses and mutations are controlled; the test does not prove HTTP caching, temporal arithmetic or durable reload.


The current-draft upstream APPEND oracle now has a bounded actual-execution companion, `TestCurrentDraftAppendRowsAgainstArangoClickHouse`. Real Group/Calculate/Filter→APPEND execution checks exact row multisets 6→6→5→6, legitimate duplicate values, nullable padding, project/generation exclusions and private capture release. Four compiled production faults produce actual wrong rows and are rejected. `node scripts/lib/formal-append-rows.mjs --out /tmp/loom-append-row-review` requires both owned databases and runs in the existing dual-store CI job. This partially closes the deleted arithmetic/cardinality guarantee; recipe authoring remains controlled and no public-save/browser/CDA claim follows. The original four→three→four oracle remains retained as its own fixture check, not counted as actual execution.


The retained operation-history profile now uses eleven explicit schedules adapted from the deleted generator. Five cases check actual service Apply and fresh public read-back; one Cancel preserves saved state, and the saved Group/Filter witness continues through mounted remounts. The independent expectation interpreter and cascade oracle remain. All eleven inputs and expected observations matched the former generator exactly before its breadth search was removed.

The other 67 schedules and optional full-profile command no longer exist in the maintained framework. They had no demonstrated additional fault detection. This is an explicit reduction of explored histories, not proof that their edit-order, repeated-key and source-availability combinations are redundant. Recover the original generator at `5646633e:verification/builder-interactions/operationHistoryModel.mjs` when a concrete defect or property needs it. All three demonstrated source-fault checks remain required evidence for changes to this retained slice. Durable storage, browser reload, actual aggregate rows and observed-transport scope remain separate.
