# Browser coverage worth recovering

The starting point is PR #32 at `899f3bb6`. The recovery point immediately before
the large retirement is `5646633e0e5c7ff8de568a4f111478d121e7f75f`.
There are 96 retired spec files: 85 ordinary specs, nine journey specs, one fast
spec, and one construction-preview benchmark. This branch initially has no `.spec.mjs` test
entry points. It retains browser workflows, native CDP checks, and the
Playwright-shaped disclosure helper. The experiment now executes that unchanged
helper on either engine. It does not claim to have migrated every retained
workflow function into an active test.

Design from the guarantee: put row algebra, scope, type, CAS, authorization and
oracle permutations in fast component or actual database/API checks. Use a
browser where input dispatch, visibility, navigation, download behavior or the
joined UI-to-service path is the missing guarantee. A second filename or CDA
dataset does not itself earn another default browser journey.

## Minimal recovery set

| Guarantee | Decision | Current boundary |
| --- | --- | --- |
| Exact table load and reload | One `preview-load` candidate consolidates `builder-preview-read-only`, `load-preview` and `builder-load` intent. | Production PreviewTable, literal rows and labels, controlled data. The former live preview request is not claimed. |
| Native row identity and input target | Keep `preview-identity` as an engine qualification check; preserve native drag in Chromium. | Existing production PreviewTable fixture. Lightpanda fails the native identity click. |
| Disclosure visibility and native typing | Execute the retained `verify-browser-disclosure.mjs` unchanged through the new engine runner. | Native summary toggle, hidden fill rejection, reopening and typing. Passes on both engines. |
| Saved route lifecycle | One `saved-route` candidate covers A/B/restored-A rows, checked route, Cancel/no-write, Apply versions, reload and late response. | Production editor/hook/panel/table with controlled responses and Apply caller. Lightpanda cannot expose route B. |
| Drag and exact file downloads | Keep the existing `scripts/browser/run.mjs` Chromium checks. | Geometry, native drag events, filename, bytes, hash, configured URL and reload. Do not substitute event injection or HTTP fetch. |
| Publication into Viewer | One small joined browser journey is worth recovering if the engine qualifies. | Actual publication/store rows have existing integration protection; complete browser render/identity remains a gap. No restoration under the failed gate. |
| Viewer filters and exports | Keep mounted client/modal checks; prioritize one joined linked-output filter/pagination/reload journey over every old permutation. | Existing exact identity and population/export checks. Joined browser obligations remain open. No restoration under the failed gate. |

The first four are executable candidates in `scripts/browser/lightpanda-cases.mjs`,
shared by both engines. They are an opt-in adoption experiment, not new mandatory
acceptance coverage. Two fail on the measured Lightpanda version. Existing
Chromium acceptance is preserved.

## Disposition of the full recovery inventory

`node scripts/audit-lightpanda-coverage.mjs` reads every spec's source and emits
the individual provenance, title/import summary, decision and current evidence.
The family counts are:

| Family | Files | Disposition |
| --- | ---: | --- |
| Small preview | 3 | Consolidate the overlapping load intent into the bounded candidate. |
| Saved route | 2 | One small candidate; keep external CDA semantics explicitly separate. |
| Native file input/download | 1 | Existing Chromium check already covers the bounded attachment obligation. |
| Legacy browser machinery and benchmark | 6 | Do not revive old driver, fixture/report catalogs, or legacy compatibility solely to run a test. Preserve useful engine assertions directly. |
| Viewer | 10 | Prefer mounted/client coverage. Preserve named browser gaps; no equivalence claim. |
| Publication | 5 | Keep actual database/compiler checks. A joined browser checkpoint remains desirable. |
| External CDA and scope families | 23 | Keep required witnesses and authorization boundaries as external gaps. Do not silently replace them with toy data. |
| Workspace | 9 | Hydration, Explorer selection, delete fallback and reload remain distinct obligations. Avoid restoring the old multipurpose suite. |
| Operation lifecycles | 37 | Keep the row/type/oracle permutations in fast and actual-engine tests. Recover only a demonstrated missing UI boundary once the engine qualifies. |

These are file-level dispositions. In particular, the 37 operation files are
not proved redundant, and the nine Workspace files are not declared covered by
the route fixture. The engine gate rejected broad restoration; the remaining
gaps in [the deleted guarantees](../DELETED_GUARANTEES.md) and
[retained coverage decisions](../RETAINED_COVERAGE_DECISIONS.md) stay open.

No formal model or formal verification skill changed. Its bounded guarantees
neither justify restoring every browser permutation nor replace a missing
browser boundary.
