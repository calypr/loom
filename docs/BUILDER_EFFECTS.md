# ExplorerBuilder effect-removal worklist

This is the source-backed worklist for removing `useEffect` from production code under `ui/packages/loom-ui/src/features/ExplorerBuilder`. It distinguishes live callsites from completed rows; a closed row retains its historical identity and closure evidence.

The original AST inventory at the 2026-10-03 snapshot found **68 `useEffect` callsites across 33 files**. A refreshed TypeScript AST walk of the current authoritative checkout after E001 closure resolves imported hooks and aliases and finds **62 current `useEffect` callsites across 32 files**. All 62 map to historical rows; `E001`, `E002`, `E003`, `E010`, `E011`, and `E068` are closed. A separate production-source scan found **2 current `useLayoutEffect` synchronization callsites**, tracked as open rows `E069` and `E070`. Tests are excluded from production-callsite counts. The worklist contains 70 stable rows: 68 historical `useEffect` rows plus the two layout-effect cleanup entries.

| `useEffect` classification | Initial | Open now | Closed |
|---|---:|---:|---:|
| User-action | 5 | 4 | 1 |
| External query/subscription | 35 | 32 | 3 |
| Render-derived | 6 | 5 | 1 |
| Props-sync | 22 | 21 | 1 |
| **`useEffect` total** | **68** | **62** | **6** |

| Separate cleanup inventory | Current | Open |
|---|---:|---:|
| `useLayoutEffect` state synchronization (`E069`, `E070`) | 2 | 2 |
| **Total open cleanup rows** |  | **64** |

The 62 live `useEffect` callsites and both layout-effect synchronization callsites remain `OPEN`. The six closed historical rows are `E001`, `E002`, `E003`, `E010`, `E011`, and `E068`. Close a row only with focused behavior regression and source review. Preserve identity checks, cancellation, cleanup, stale-response rejection, and user-action boundaries recorded in `source_evidence`. Current file/line anchors point to the live site or its replacement owner; `historical_source_anchor` preserves the original inventory location.

`E001` now persists the selected output from the user selection action and successful table create, duplicate, and selected-delete completion paths; local duplicate and delete fallback paths retain the same ownership. Focused selection and populated-table tests and TypeScript test checking passed. The native tables report `/tmp/loom-selected-table-event-owner-dialog-fixed.json.tables` passed all required checks with 11 timed actions, a 1076 ms maximum, no errors, and unchanged source fingerprint `5d8ab7552022b1006b866c78496e89b51104abd044c3b5afd6097320b61c6323` across 1120 files.

`E002` now uses `useResolveConfiguredColumnContextsQuery` at `react.tsx:177`, called by `BuilderWorkspace.tsx:564`. Its real compound-coded browser report at `/tmp/loom-coded-group-effect-free-context/compound-coded-qa-1791073952586.json` has `failures: []`, with 12 timings and a maximum of 1519 ms. The focused UI run at `/tmp/loom-effect-free-context-integrated-tests.log` passed 52 tests across four files; the parent also reported the worker's full 530-test suite passed.

`E003` no longer invalidates the applied-choice preview cache from a render effect. `matchesAcceptedChoicePreview` in `constructionWorkspace/appliedChoicePreview.ts:21-41` validates owner, output, limit, snapshot, receipt, workspace digest, and rows at the reuse boundary (`BuilderWorkspace.tsx:2218-2226`). `appliedChoicePreview.unit.test.ts:42-59` covers a valid reuse and rejects each identity mismatch.

`E068` no longer has an effect callsite in `useAutomaticPreview.ts`; the hook delegates to `useKeyedQuery` at `:41`, keyed by the request identity. `react.tsx:141-145` keys the query resource, whose owner aborts superseded work (`:75-92,104-113`). `useAutomaticPreview.unit.test.tsx:24-89` covers same-key reuse, cancellation and same-key retry, and explicit reload. The registered first-table and Recompile browser cases were reported green on source fingerprint `2d9d34eab308a7951979cd6ee48ae06548bad9fb4655322c6bf1c1de31de9bc3`.

`E010` no longer copies a handed-off selection into local state. `BuilderWorkspace.tsx:938-940` derives the active population from the current override, prop, or keyed query result. `E011` resolves attached and saved-cohort selections through the query owner at `react.tsx:213`; its caller passes project, explorer, authorization path, snapshot, generation, output, resource type, and attached/cohort identities. Reconciliation tests cover stale table switches, saved-cohort source restoration, missing metadata, and handed-off selection precedence. The parent reports 28 focused tests and typecheck passed. The native report `/tmp/loom-cohort-source-collection-effect-free/report.json` passed 28 cases with a maximum duration of 1987 ms, no errors, unchanged source across 1057 watched files, and an unchanged API build.

No remaining callsite is exempted by a permanent baseline. Do not replace `useEffect` with `useLayoutEffect` to satisfy this worklist. The final static rule should reject every `useEffect` call expression in this production scope using AST resolution for imported aliases and `React.useEffect`; it must not accept an allowlist of existing calls. The gate is not added by this artifact.

The integrated preview owner also passes the current CDA starting-collection /
cohort lifecycle: `/tmp/loom-cohort-source-collection-preview-owner-green/report.json`
records28 steps, max1890ms, errors[], an independent exact-scope raw Specimen
oracle, unchanged1057-file source freeze and unchanged fresh API build. This
adds Apply/Cancel, upstream revision/collection changes, reload and restoration
evidence for the shared preview owner; it does not close other effect rows.
