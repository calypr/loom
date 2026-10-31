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
