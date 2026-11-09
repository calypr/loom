> Current status: this map retains user guarantees. Its old native-bracket/browser journey commands are historical and blocked until a replacement is proven. Use the existing formal verification feature map and its behavior/API routes for executable checks; those do not by themselves close browser or durable-storage guarantees. Core development doctor commands have been repaired; retired browser journey commands reject explicitly.

# Loom verification feature map

This index describes verification entry points, not a claim that every Builder
state passes. Native Playwright assertions establish a case result; retained
reports identify its source, API build, data scope, and remaining gaps.

| Feature | User path and recipe |
|---|---|
| [Authoring](authoring.md) | Create a table, choose or replace its starting records, select fields, and inspect settled rows. |
| [Preview and publication](publication.md) | Check the current draft, publish it, and verify the same output in Viewer after reload. |
| [Viewer](viewer.md) | Inspect evidence, filter published rows, and download a pinned dataset. |
| [Development iteration](iteration.md) | Prove source ownership, UI HMR, backend rebuild, failed-build shutdown, and recovery. |

The four pages are entry points. The complete visible Builder inventory,
including Group, Pivot, Unpivot, repeated and related rows, source policies,
cohorts, Filter, Combine, upstream edits and removal, is in
[BUILDER_SURFACE_INVENTORY.md](../../../../docs/BUILDER_SURFACE_INVENTORY.md).
The active queue is [BUILDER_WORKLIST.tsv](../../../../docs/BUILDER_WORKLIST.tsv).
Exact lifecycle evidence is recorded in
[BUILDER_VERIFICATION.tsv](../../../../docs/BUILDER_VERIFICATION.tsv).
Do not infer CDA coverage from a Basic fixture pass or full transition coverage
from one operation's pass.

Use [the registry](../../../../scripts/verify-ui/registry.mjs) to select a
scenario and case. Follow [the verification skill](../SKILL.md) for target
binding, source freezes, independent oracles, diagnostics and latency budgets.
Workflow libraries are not standalone Node browser launchers.

Historical notes are retained in [history/20261011-feature-map.md](history/20261011-feature-map.md).
They preserve earlier evidence and failures; their commands are not current recipes.

## Maintenance checkpoint

2026-10-11: all four feature pages received source review and live attempts.
The corrected links and case selections passed independent source review.

| Live check | Result and scope |
|---|---|
| CDA publication | Simple Specimen and expanded Observation component cases passed, including exact rows, publication identity and Viewer reload. |
| Viewer filter/export | Registered Basic filtered-population ZIP case passed 10/10 checks; selected facet did not truncate the complete authorized archive. |
| Source iteration | Native `make verify-current` passed in 29.7 seconds, including HMR/build failure/recovery probes and source restoration. |
| Authoring | After correcting the stale `base` expectation to `current`, the same native case passed 22/22 checks, including root changes, Cancel/Apply, restoration and reload. |
| Broad full journey | Deterministic owner-record selection and both inspector paths pass, as do reusable mapping checks. The contributor race fix passes 39 focused tests and its Equals entry/save path passed a native attempt. The final cold-cache native verify-full passed in 77.963 seconds, including exact rows, publication, filtering, ZIP, Viewer reload, saved and visible contributor reload, source iteration and final diagnostics. No optimizer reload occurred during the test. |

Maintenance outcome: **passed within the documented Basic/CDA case scopes**.
The contributor race is fixed locally and the full Basic journey passed. This
maintenance checkpoint does not close the paused Builder goal or establish
uncovered CDA transitions. Earlier scoped CDA reports retain their recorded
source identities; they are not fresh reruns of the final Basic checkpoint.

Retained bracket summaries: authoring RED `run-builder-authoring-authoring-rZUJEc`, authoring GREEN `run-builder-authoring-authoring-1jkVKI`
and Viewer `run-viewer-query-dataset-zip-filtered-complete-population-e2YnMD`
in the session's `loom-verification-brackets` temporary directory. Iteration
evidence is copied to `/private/tmp/loom-feature-map-maintenance-20261011/verify-current`.
Later full-journey errors and diagnostics are retained under
`/private/tmp/loom-feature-map-maintenance-20261011/full-scroll-movement-rerun`.
The first full-journey terminal error and locator stack were captured in this session;
its default Playwright artifact directory was overwritten by the subsequent
iteration run, so retained original full-journey artifacts are not claimed.

### Contributor input correction

The full journey selects Observation `status`, then “Equals a value”. The
retained DOM instead shows “Has a value” and no contributor-value input.
The editor remains interactive during the previous contributor save; its
current-column effect resets local condition state when that save is acknowledged.
The verifier must not wait around or repeat the selection to hide this race.

The retained failure is in
`full-scroll-movement-rerun/artifacts/dev-journeys-Loom-developm-d1e17-ull-dev-journey-verify-full/error-context.md`
under the maintenance evidence directory above. Relevant product code is
`FeaturePolicyEditor.tsx`'s `ContributorEditor` and the Builder command-result
reconciliation. The editor now preserves newer input when an earlier save is acknowledged,
and configured rows are keyed by output identity. The regression fails on the
original editor and passes with the fix; the two focused specs pass 39/39.
The native `full-contributor-race-fixed` attempt passed the Equals entry/save
path before stopping at a date selector. The final `full-diagnostics-cold-complete-fixed` native attempt passed
saved and visible EQUALS `final` after a real Builder reload, independent
preview and published rows, explanation, filtering, ZIP validation, Viewer
reload and iteration probes. Its three deliberate 422 responses are bound
to exact owned URLs, codes and action windows. Canceled reconcile requests
compile already saved drafts; the existing request classifier treats them as
reads. Failed or canceled actual command writes remain fatal.

The earlier invalid-hook error coincided with late Vite dependency discovery.
The demo now eagerly prebundles its six linked-package runtime dependencies.
After clearing only the owned UI's generated optimizer cache and restarting
that UI, the full journey passed without dependency-optimization reloads;
startup cache metadata remained unchanged. No unexpected HTTP, console,
network or page errors were observed. Source/API identity and post-run doctor
passed; unrelated staged changes were preserved.

Final evidence:
`/private/tmp/loom-feature-map-maintenance-20261011/full-diagnostics-cold-complete-fixed/artifacts/mv4o7cme-593bd915/report.json`.
The same run's `ui-logs-since-restart.log` retains the cold-optimizer evidence.
