# UI plumbing execution report

BASE `468fa27eae1d3a9d0f58fb237407d048c4173f1d`. Verified code snapshot `a2cf34edbd58c7e38c160115fc05e168b5b56ae2`.

The local branch moves Builder data requests and workflow state behind the session bindings and groups the views by domain. The package boundaries enforce zero unreachable production files, unused exports, cycles, cross-layer imports, UI client access, and draft identity comparisons outside their owners. Four imperative DOM subscriptions remain inside the exact allowlist. The browser portion of acceptance is deferred because the local runtime cannot open its mounted source files.

The semantic identity matchers retain their original client and catalog behavior. The original implementations agree on supported exact concepts and system/version categories. Differential tests also reproduced eight malformed or incomplete cases that the wire schemas reject. Client matching accepts empty, missing, or null category families on both sides and an unknown identity kind with an otherwise matching category. Catalog matching rejects those four cases. Catalog matching accepts a missing concept or binding paired with null, distinct concept objects that serialize identically, and two NaN concepts. Client matching rejects those four cases. Each case asserts both original results and marks proposed matcher parity with `it.fails`. The shared derivation and construction route comparison remain factored in contracts.

The BASE layer count came from report mode before the target directories existed. The final scan uses the same outer layer rules plus the enforced Builder model, session, DOM, and UI boundaries. Source shapes remained broad in some keyed form controls. The per-name width budgets record those boundaries explicitly.

## Ledger status

| Phase | Done | Obsolete | Deferred |
| --- | --- | --- | --- |
| P0 | 5 | 0 | 0 |
| P1 | 5 | 0 | 0 |
| P2 | 9 | 0 | 1 |
| P3 | 2 | 0 | 0 |
| P4 | 48 | 0 | 4 |
| P5 | 8 | 0 | 2 |
| New rows | 8 | 0 | 0 |

## Deferred rows

| Row | Reason | Retry evidence |
| --- | --- | --- |
| P2.identity-merge | operator decision: implementations diverge | Retry executed original client/catalog and merged matchers. Eight malformed cases diverge. Original behavior restored in separate contracts functions; selecting a single behavior requires operator decision. |
| P4.2 | browser acceptance physically blocked by the existing development bind mount | Implementation committed and non-browser checks passed. Final retry stopped in dev-doctor before Chrome. Air could not open /workspace/go.mod or /workspace/go.sum. Restore the development bind mount or use a healthy local runtime to run browser acceptance. |
| P4.4 | browser acceptance physically blocked by the existing development bind mount | Implementation committed and non-browser checks passed. Final retry stopped in dev-doctor before Chrome. Air could not open /workspace/go.mod or /workspace/go.sum. Restore the development bind mount or use a healthy local runtime to run browser acceptance. |
| P4.6 | browser acceptance physically blocked by the existing development bind mount | Implementation committed and non-browser checks passed. Final retry stopped in dev-doctor before Chrome. Air could not open /workspace/go.mod or /workspace/go.sum. Restore the development bind mount or use a healthy local runtime to run browser acceptance. |
| P4.7 | browser acceptance physically blocked by the existing development bind mount | Implementation committed and non-browser checks passed. Final retry stopped in dev-doctor before Chrome. Air could not open /workspace/go.mod or /workspace/go.sum. Restore the development bind mount or use a healthy local runtime to run browser acceptance. |
| P5.target-tree | operator-owned untracked file | The replacement UI section is in the execution report. Operator ownership prevents editing the original file. |
| P5.browser | browser acceptance physically blocked by the existing development bind mount | Implementation committed and non-browser checks passed. Final retry stopped in dev-doctor before Chrome. Air could not open /workspace/go.mod or /workspace/go.sum. Restore the development bind mount or use a healthy local runtime to run browser acceptance. |

## Package measurements

| Metric | BASE | Final code |
| --- | --- | --- |
| Production physical lines | 50738 | 49286 |
| Test physical lines | 56782 | 56518 |
| Unreachable production files | 9 | 0 |
| Unused exports | 203 | 0 |
| Test-only exports | 68 | 22 |
| Import cycles | 1 | 0 |
| Layer violations | 0 | 0 |
| Identity comparison lines | 131 | 23 |
| Structural identity comparisons | 153 | 25 |
| AbortController construction sites | 27 | 4 |
| Effect-like sites | 16 | 4 |
| BuilderWorkspace.tsx physical lines | 2369 | 856 |
| AbortController sites outside client/data | 27 | 0 |
| Identity comparisons outside approved owners | 131 | 0 |
| React effect baseline | {} | {} |

## Prop and option widths

| Measure | BASE | Final code |
| --- | --- | --- |
| Widest JSX attribute bags | `OperationEditorHost` (57), `RowDefinitionSettingsPanel` (47), `ImportedSourceSteps` (37), `ConceptCatalogContent` (35), `GuidedGraphWorkspace` (33), `ConstructionWorkspace` (32), `ConceptCatalog` (31), `AuthoredTraversalEditor` (28), `SourceStepParameterEditor` (26), `TraversalScopeNavigator` (24) | `SourceStepParameterEditor` (26), `SourceRelationshipReview` (22), `PivotEditor` (21), `ConstructionReshapeEditor` (19), `section` (19), `GroupEditor` (18), `CreateColumnRule` (16), `FilterRowsEditor` (15), `ReactFlow` (15), `RelatedExpandContributorEditor` (15) |
| Widest hook option bags | `useCommandQueue` (25), `actionsFor` (24), `useAddColumns` (24), `useTableExplorerCrud` (22), `usePublish` (16), `useRootChange` (16), `usePopulationExclusion` (11), `useReshapeAvailability` (11), `useReconcile` (9), `useResolvePopulationSelectionQuery` (9) | `useCodedPivotCategories` (11), `usePopulationMemberRemoval` (11), `useFrameCategories` (10), `useFrameSourceOptions` (10), `useRelatedExpandRoutes` (10), `usePublish` (9), `useRelatedEligibilityChoices` (9), `useResolvePopulationSelectionQuery` (9), `useAddColumns` (8), `useCodedPivotSources` (8) |

The width ratchet uses raw JSX attributes and binding fields. It includes keys, accessibility attributes, presentation options, and library props. The per-name budgets cap the remaining raw widths and apply limits of ten JSX attributes and eight hook options to new names. `useAddColumns` and `useTableExplorerCrud` each take eight owner bindings after their mirrored inputs were removed.

| Remaining raw-width group | Reason the attributes stay on the boundary |
| --- | --- |
| `SourceStepParameterEditor`, `CreateColumnRule`, `ImportedSourceSteps`, `RecipeColumnPolicyInspector` | Separate rule and source-step instances select different saved values, presets, active controls, and edit actions. Controlled form inputs and their local presentation stay together. |
| `SourceRelationshipReview`, `RouteExtensionPanel`, `RelatedStepEditors` | Relationship, rule, and saved source-step review instances supply distinct proposals, summaries, validation and apply callbacks. |
| `ConstructionReshapeEditor`, `PivotEditor`, `GroupEditor`, `FilterRowsEditor`, `UnpivotEditor`, `CodedPivotEditor`, `CodedPivotEditorBody`, `ConstructionCombineEditor` | Each keyed form instance receives the active operation, stage capabilities, editor-specific controls and field callbacks. Loading, confirmation, and empty-state attributes affect their presentation. |
| `RelatedExpandContributorEditor`, `RelatedExpandEditor`, `RelatedFieldEditor`, `RelatedEligibilityEditor`, `RelatedSourceStepEditor` | Each related operation supplies its own anchor, route, contributor scope and field configuration. The query sessions own request execution. |
| `FrameSourcePanel`, `PopulationMemberRemoval`, `CatalogItemRow`, `CatalogSelectionOptions`, `FacetFilter`, `FormulaOperand` | The parent configures the current frame, collection revision, catalog item, filter, or formula operand. A mounted instance also receives the display state and handlers for its controls. |
| `RowDefinitionSettingsPanel`, `TableCreatorPanel`, `GuidedGraphWorkspace` | Layout controls and focus targets remain distinct across editing contexts. Session selectors supply their shared draft and action values. |
| `ReactFlow`, `Select`, `section`, `aside`, `path`, `button` | The raw width includes external library interfaces, JSX accessibility attributes, native events, and styling. |

The hook exceptions are `useCodedPivotCategories`, `usePopulationMemberRemoval`, `useFrameCategories`, `useFrameSourceOptions`, `useRelatedExpandRoutes`, `useRelatedEligibilityChoices`, and `useResolvePopulationSelectionQuery`. These adapters expose the precise route, revision, output, and paging inputs that make up their request or staleness identities. `usePublish` has nine options for readiness and actions supplied by the independently scoped publication, recipe, construction, and reconciliation owners. The individual widths are in the enforcing graph baseline.

## Behavior and trace comparison

The original BASE captures remain in `.cache/uiplumb/oracle/base/` and `base-repeat/`. Identical source runs differed in transient DOM and request counts. The operator approved the revised G3 in `d4799c609`. It uses protected journey assertions, targeted request and race tests, and browser milestones. Raw traces remain diagnostic.

The final trace matched 1566 tests by identity (348 by full path and the rest by unique filename and test name). It recorded 130 matching tests with a DOM or event difference and 64 with a call-log difference. There were 34 unmatched BASE tests and 56 unmatched final tests. The individual observations are in `.cache/uiplumb/rescan/final-trace-diagnostic.json`.

G3 abort-removal ledger entries: 0.

The two immutable captures from the same BASE source differed in 63 DOM or event traces and two call logs. The final comparison preserves every raw difference for review. In the matching tests that had call differences, the aggregate trace includes 57 more authoring-network reads, three more capabilities reads, and three more semantic-browse reads. Most reuse request bodies seen in BASE. Five read payloads, excluding generated request IDs, appear only in the final sample. These concern current output, revision, stage, or catalog admission during the tested transitions.

The trace plugin wraps direct `useLoomClient()` reads. After the mutation binding moved to `useOptionalLoomClient()`, the formal reconciliation race no longer appears under that proxy. The unchanged journey still captures exactly two actual transport requests with the expected draft and output identities. Saved-view writes now appear through a direct client hook. The BASE and final test callbacks assert the same save, delete, and adoption behavior. The final capture covers all 1,622 executed tests, including the expected identity divergence cases.

The raw comparison is diagnostic under the revised G3. The final browser blocker leaves real-Chrome parity unverified.

## Verification

| Check | Exit | Seconds | Log |
| --- | --- | --- | --- |
| make e2e | 2 | 30.4 | final-refactor-e2e.log |
| env VITEST_MAX_WORKERS=3 LOOM_UI_TRACE=/Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/oracle/final-refactor make test-ui | 0 | 93.6 | final-refactor-ui.log |
| npm --prefix ui run build | 0 | 44.2 | final-refactor-build.log |
| python3 /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/check-traces.py /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/worktrees/final-refactor /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/oracle/final-refactor /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/logs/final-refactor-ui.log | 0 | 1.3 | final-refactor-trace-coverage.log |
| python3 /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/compare-final.py /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/oracle/base /Users/peterkor/Desktop/BMEG/loom/.cache/uiplumb/oracle/final-refactor | 0 | 1.9 | final-refactor-trace-comparison.log |

- The package boundary check validates the manifest, all four entry points, and the stylesheet export. The original generated BASE manifest loses exactly the same 23 Builder internals from the root and Builder entry points. Viewer and saved views retain their exports. The package version is 0.2.0.
- A read-only `git grep` of IDP-Frontend `feature/clickhouse-explorer` found three consumed root names and eleven consumed saved-views names. All remain exported at their original entry points. The stylesheet subpath is present.
- The local runtime reached `dev-doctor` and could see the mount metadata. Air's `sha256sum` could not open `/workspace/go.mod` or `/workspace/go.sum`. Chrome never started. The Builder and CDA browser cases from the verify skill did not run. Restoring the development bind mount or providing another working local runtime unblocks browser acceptance.
- The repository's Go package audit helper has no `ui/packages/loom-ui` row. Its focused package command did not run Go tests. The UI import graph captures the TypeScript dependency changes.
- During Phase 5, the guard failed as expected in a disposable copy with an added UI client import, a raw staleness comparison outside its owner, and a duplicate DOM subscription.
- A BASE-to-final callback comparison covered the 18 located protected Builder, Viewer, saved-view, and mounted adapter files and 261 test callbacks. Comparison normalized the moved import literals and one equivalent transport argument type. It found no other callback changes.
- A disposable width probe also rejected 11 JSX attributes and nine hook options on new names. The largest remaining raw widths are held to their individual final budgets.
- External interrogate runners were unavailable for the later units. Independent same-model reviews checked the exact candidate diffs, affected callers and available runtime witnesses. The ledger records review evidence by unit.

## Decisions

| Row | Decision | Reason |
| --- | --- | --- |
| P0.1 | Run full suites serially in an isolated worktree with npm ci --offline | 10 CPUs, 64 GiB physical RAM, existing dnd symlink loaded a different React. Fresh locked install passed the failing file. |
| P0.1 | Keep baseline at 468fa27eae1d3a9d0f58fb237407d048c4173f1d | This was the actual branch tip at startup. The plan commit is earlier. |
| P5.target-tree | Provide replacement UI section in the report | TARGET_TREE.md was untracked before execution and is operator-owned. |
| P0.1 | Normalize generated Mantine IDs in HTML identity attributes and associated references | All seven saved-view tests differed at randomId-generated values between unchanged-source runs. Mantine uses nine base36 characters. |
| P0.1 | Observe client acquisition in trace-only Vitest transform as well as shared mock factory | Hoisted operation-history fixtures and real-provider empty-project fixtures otherwise produced unobserved requests. |
| P0.1 | Capture test-end using runner before suite cleanup and write after cleanup | Explicit per-suite cleanup would otherwise remove DOM before the final observation. |
| P0.1 | Compare before any production edit | Both unchanged-source runs passed all 1600 tests. The repeat had 63 DOM/event differences and two call-log differences. |
| P0.1 | Keep the original base trace and require G3 evidence for every removal | A second settled describeAuthoringNetwork request disappeared in a protected Builder journey. |
| P0.1 | Use native single-worker execution as a retry | Fixed test selection and one worker isolate scheduling without altering timer or render semantics. |
| P0.1 | Keep clock and frame experiments in disposable checkouts | A virtual clock removed settled requests; frame draining failed to reach quiescence and fixed-frame execution changed captured geometry. |
| P0.2/P0.3 | Measure both the original graph scope and testSupport importers | DescribeAuthoringNetworkArgs is used by ordinaryNetworkFixture, changing the unused/test-only split by one. |
| P0.2 | Report physical source lines excluding declaration files separately | The original source-line baseline includes test-matchers.d.ts, and the workspace audit counts the trailing empty segment. |
| P0.4 | Generate the full four-entry surface and validate consumption separately | The plan restricts future changes to G1 removals; demo and frontend contract names are only a subset. |
| P4/P5 | Leave the development stack untouched after the preflight failed | The running API container cannot find the mounted Go module files. Deployment changes fall outside this assignment. |
| P0.1 | Preserve original compressed captures and run normalization candidates in isolated worktrees | Additional identifier normalization can account for generated values but cannot account for documented geometry and request changes. |
| P0.1 | Use protected journeys and targeted response contracts as acceptance | Operator authorized revised G3. Preserve raw traces for diagnosis. |
| P4.1.1 | Keep React batching during draft snapshot updates | The protected source-step Apply case loses focus when an external-store notification renders a new identity before recipe completion. |
| P4.1.2 | Move remembered-table storage policy into session | Keep existing owner key and read/write schedules, including unavailable storage fallback. |
| P4.1.3 | Inject a narrow bound Builder draft client from data | The Builder client access ratchet flagged three direct dependencies. Data owns provider access while the session owns request lifetime. |
| P4.2.1 | Share revision comparison only | Keep owner, proposal, output, limit and preview-generation guards specific to each call path. |
| P4.1.3 | Bind each refetch to its owning session | A captured refetch retains the client and owner scope that created it. No affected caller regression in the protected cases. |
| P4.1.5 | Settle source recipe before publishing saved draft | The prior synchronous store candidate discarded its focus initiator. The unchanged focus journey passed after settlement was made part of the command commit. |
| P4.1.6 | Observe server query identity only when data key changes | An unchanged query snapshot incorrectly replaced explicitly refreshed draft metadata after moving the subscription boundary. |
| P4.2.4 | Append command before notifying pending subscribers | The session store notifies synchronously. Queue state is installed before an observer can reenter. |
| P4.2.6 | Keep command writes serialized while using cancellation slots for reads | Aborting an admitted write can leave an applied server change unobserved; preserve pause/drain and settlement ordering for the write queue. |
| P4.2 | Continue after the browser prerequisite failed | The host-level make e2e stopped in dev-doctor because the Air container is missing the source mount. Focused Builder and CDA browser cases did not run. |
| P4.3.4 | Keep the active proposal resource responsible for retirement | The unchanged A→B→A schedule failed when a brief capabilities switch immediately canceled proposal A. Its resource stays active when re-subscribed before cleanup; the focused test passed after restoring this ownership. |
| P4.3 | Retain mounted construction adapter schedule coverage | The deleted root and capability hook assertions were ported. Existing mounted construction schedules still drive the live binding and catch A→B→A, Apply and cancellation behavior. |
| P4.4.2 | Keep DOM hosts and graph highlight local | The three DOM hosts and the graph highlight are presentation state. Explorer selection stays in its route boundary so it can construct the scoped session. |
| P4.4.2 | Store workspace state that is read across panels or by actions | Notices, review focus, population overrides, interpretation refresh and proposal cancellation are shared; the occurrence panel cache retains its original owner/output/revision key. |
| P4.4.3 | Capture first-table title on action invocation | The unsubmitted input remains local. The async session action receives the clicked title and a clear callback and owns pending progress. |
| P4.6.3 | Retire Coded Pivot callbacks on unsubscribe | Review reproduced a late old source auto-selection before deferred transport cancellation. Separate callback invalidation from microtask abort. |
| P4.6 | Browser runtime still blocked by inaccessible Container bind mounts | Docker lists both Go files in Air container, but sha256sum opens fail ENOENT on both. Repair requires runtime remount or recreate outside approved changes. |
| P5.ui-domains | Move cross-domain pure helpers to model before relocating views | Session and model share reshape evaluation and construction interpretation; the UI remains a consumer. |
| P5.focus | Bind only the two real focus destinations | Table name and row-type search exist in the creator; other historical selectors had no production matches. |
| P5.position | Preserve the browser rect formula and capture-phase scroll | Update the observable position from the same anchor, viewport dimensions and listeners. |
| P5.escape | Preserve native document and window phases in one coordinator | Document dismissals precede the outer window handler and still allow a window-only keydown. |
| P5.layer-enforce | Apply the ownership count to Builder ui/ and enforce an exact DOM path multiset | The view directory now exists. The negative control produces client, ownership and duplicate subscription failures. |
| P2.identity-merge | Restore distinct client and catalog matchers | The original valid-wire table missed eight divergence cases for rejected runtime inputs. The mechanical decision rule still applies. |
| N-7 | Pass existing navigation, selection, preview and first-table owners to their consumers | The same owners already produce these values. The unchanged UUID helper now serves its two callers directly. |
| N-8 | Select shared frame and population draft fields from the Builder session | Frame/table UI owns its controls and callbacks. Draft Explorer identity remains distinct from route query identity during admission. |
| N-8 | Cap remaining named widths after measured reductions | New bags enforce 10 JSX attributes and 8 hook options. Individual existing ceilings document layout or distinct scope needs and cannot grow. |
| N-8 | Reduce Vitest concurrency for the retry | The only failure was the unchanged 15-second batch test. It passed in isolation with its original assertions and deadline; cap worker count for full verification. |
| P5.browser | Defer browser-only acceptance after the final exact-source retry | Air opened neither module file from the mounted source and dev-doctor exited before Chrome. |
| Final trace | Keep the full trace differences as diagnostic evidence | Direct useLoomClient proxy observes different mutation bindings. The unchanged real reconcile transport and saved-view assertions passed. Additional reads are recorded by identity for review. |

## Test assertion ports

155 entries, including 90 no-longer-applicable assertions. The full mapping is in `.cache/uiplumb/test_port_map.tsv`.

| Old test | Assertion | Reason |
| --- | --- | --- |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > groups value settings separately from server-resolved time and unit settings | expect(screen.getByRole('group', { name: 'Values' })).toBeTruthy() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > groups value settings separately from server-resolved time and unit settings | expect(screen.getByRole('group', { name: 'Time and units' })).toBeTruthy() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > groups value settings separately from server-resolved time and unit settings | expect(screen.getByRole('group', { name: 'Values' }).textContent) .toContain('Category recoding is unavailable until the server resolves this column capability.') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > groups value settings separately from server-resolved time and unit settings | expect(screen.getByRole('group', { name: 'Time and units' }).textContent) .toContain('Choose a date-aware value selection to configure its time window here.') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves unsaved category edits across unrelated rerenders | expect(screen.getByRole('textbox', { name: 'Recorded category 1 for Patient gender' })) .toHaveProperty('value', 'draft female') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves unsaved category edits across unrelated rerenders | expect(screen.getByRole('combobox', { name: 'Unmapped value policy for Patient gender' })) .toHaveProperty('value', 'KEEP_ORIGINAL') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('textbox', { name: 'Recorded category 1 for Patient gender' })) .toHaveProperty('value', 'male') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('textbox', { name: 'Replacement value 1 for Patient gender' })) .toHaveProperty('value', 'M') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('combobox', { name: 'Unmapped value policy for Patient gender' })) .toHaveProperty('value', 'KEEP_ORIGINAL') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('textbox', { name: 'Recorded category 1 for Patient active' })) .toHaveProperty('value', 'true') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.queryByRole('textbox', { name: 'Recorded category 1 for Patient active' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('combobox', { name: 'Unmapped value policy for Patient active' })) .toHaveProperty('value', 'ERROR') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > resets category drafts when the saved recode changes, the column switches, or recoding is removed | expect(screen.getByRole('textbox', { name: 'Recorded category 1 for Patient active' })) .toHaveProperty('value', '') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(screen.getByRole('checkbox', { name: 'Include start boundary' })) .toHaveProperty('checked', true) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(screen.getByRole('checkbox', { name: 'Include end boundary' })) .toHaveProperty('checked', false) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(screen.getByRole('spinbutton', { name: 'Look back days' })) .toHaveProperty('value', '2') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(screen.getByText(/start is inclusive and end is exclusive\./).textContent) .toContain('from 2 days before the row date to the row date') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(onSourceChange).toHaveBeenCalledOnce() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > reloads a saved exclusive end boundary and reapplies the saved temporal values | expect(onSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'FIRST_ORDERED', path: 'valueQuantity.value', contributorWindow: { timestampPath: 'effectiveDateTime', anchorPath: 'meta.lastUpdated', lowerOffsetSeconds: -172_800, upperOffsetSeconds: 0, lowerInclusive: true, upperInclusive: false, precision: 'INSTANT', }, ordering: { timestampPath: 'effectiveDateTime', direction: 'DESC', tiePolicy: 'REQUIRE_UNIQUE', }, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(observationSettings.getByRole('combobox', { name: 'Date selection direction' })).toBeTruthy() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(observationSettings.getByRole('combobox', { name: 'Equal date handling' })).toBeTruthy() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(startBoundary).toHaveProperty('checked', true) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(endBoundary).toHaveProperty('checked', true) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(startBoundary).toHaveProperty('checked', true) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(endBoundary).toHaveProperty('checked', false) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(observationSettings.getByText(/start is inclusive and end is exclusive\./)).toBeTruthy() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(otherSettings.queryByRole('button', { name: 'Apply date selection' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(onObservationSourceChange).toHaveBeenCalledOnce() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(onObservationSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'FIRST_ORDERED', path: 'valueQuantity.value', contributorWindow: { timestampPath: 'effectiveDateTime', anchorPath: 'meta.lastUpdated', lowerOffsetSeconds: -172_800, upperOffsetSeconds: 0, lowerInclusive: true, upperInclusive: false, precision: 'INSTANT', }, ordering: { timestampPath: 'effectiveDateTime', direction: 'DESC', tiePolicy: 'REQUIRE_UNIQUE', }, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an inclusive start and exclusive end for one column only | expect(onOtherSourceChange).not.toHaveBeenCalled() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an exact date window on a related SUM without changing its unit preset | expect(screen.queryByRole('combobox', { name: 'Date selection direction' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an exact date window on a related SUM without changing its unit preset | expect(screen.queryByRole('combobox', { name: 'Equal date handling' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > authors an exact date window on a related SUM without changing its unit preset | expect(onSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'SUM', path: 'valueQuantity.value', contributorWindow: { timestampPath: 'effectiveDateTime', anchorPath: 'meta.lastUpdated', lowerOffsetSeconds: -259_200, upperOffsetSeconds: 0, lowerInclusive: true, upperInclusive: false, precision: 'INSTANT', }, unitNormalization: { policyId: 'to-centimeters', version: '2' }, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > keeps a compatible window and normalization when changing SUM to MEAN | expect(onSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'MEAN', path: 'valueQuantity.value', contributorWindow: savedWindow, unitNormalization: { policyId: 'to-centimeters', version: '2' }, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > keeps a compatible window and drops normalization when changing SUM to COUNT | expect(onSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'COUNT', path: 'valueQuantity.value', contributorWindow: savedWindow, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > keeps the window and removes ordering when changing FIRST_ORDERED to SUM | expect(onSourceChange).toHaveBeenCalledWith({ kind: 'aggregate', aggregate: { operation: 'SUM', path: 'valueQuantity.value', contributorWindow: savedWindow, }, }) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > does not offer unit normalization for COUNT or EXISTS | expect(screen.queryByRole('button', { name: 'Normalize units' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > does not offer unit normalization for COUNT or EXISTS | expect(screen.queryByRole('button', { name: 'Edit unit normalization' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > does not persist a canceled date-window edit | expect(onSourceChange).not.toHaveBeenCalled() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > does not persist a canceled date-window edit | expect(savedColumn.source).toEqual(sumColumn(savedWindow).source) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the exact temporal capability refusal for a related reducer | expect(within(screen.getByRole('group', { name: 'Time and units' })) .getByRole('status').textContent).toBe(`Unavailable: ${reason}`) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the exact temporal capability refusal for a related reducer | expect(screen.getByRole('button', { name: 'Add date window' })).toHaveProperty('disabled', true) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the observed unit evidence behind normalization choices | expect(evidence).toHaveTextContent('Body height · http://loinc.org · 8302-2') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the observed unit evidence behind normalization choices | expect(evidence).toHaveTextContent('Observed units: cm, m · additional units exist') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the observed unit evidence behind normalization choices | expect(evidence).toHaveTextContent('Observed examples: 170, 172') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > shows the observed unit evidence behind normalization choices | expect(evidence).toHaveTextContent('denominator and current-table coverage are not provided') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > marks fallback aggregate choices as unverified when source capabilities are missing | expect(screen.getByText( 'Server-supported aggregate choices are unavailable for this source; fallback choices need validation by Loom.', )).toBeInTheDocument() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > marks fallback aggregate choices as unverified when source capabilities are missing | expect(screen.getByRole('option', { name: 'Count values' })).toBeInTheDocument() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(onContributorChange).toHaveBeenCalledWith(existsAcknowledgment) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(screen.getByRole('combobox', { name: 'Contributor condition for Has Observation' })).toHaveProperty('value', 'EQUALS') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(screen.getByRole('textbox', { name: 'Contributor value for Has Observation' })).toHaveProperty('value', 'female') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(screen.getByRole('combobox', { name: 'Contributors for Has Observation' })).toHaveProperty('value', categoryRecodeCandidate.candidateId) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(screen.getByRole('combobox', { name: 'Contributor condition for Has Observation' })).toHaveProperty('value', 'EQUALS') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > preserves newer contributor edits when an earlier submission is acknowledged | expect(screen.getByRole('textbox', { name: 'Contributor value for Has Observation' })).toHaveProperty('value', 'female') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > adopts a later external contributor after replacing an older pending value | expect(screen.getByRole('combobox', { name: 'Contributors for Has Observation' })) .toHaveProperty('value', categoryRecodeCandidate.candidateId) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > adopts a later external contributor after replacing an older pending value | expect(screen.getByRole('combobox', { name: 'Contributors for Has Observation' })) .toHaveProperty('value', statusCandidate.candidateId) | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > adopts a later external contributor after replacing an older pending value | expect(screen.getByRole('combobox', { name: 'Contributor condition for Has Observation' })) .toHaveProperty('value', 'EXISTS') | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| FeaturePolicyEditor.unit.test.tsx :: FeaturePolicyEditor > adopts a later external contributor after replacing an older pending value | expect(screen.queryByRole('textbox', { name: 'Contributor value for Has Observation' })).toBeNull() | Reason no longer applies: the tested FeaturePolicyEditor and its only production dependency were confirmed unreachable from package entries and have been deleted. |
| columnLabel.unit.test.ts :: normalizedColumnLabel > trims a non-empty label and restores the saved label when empty | expect(normalizedColumnLabel(' Zulu ', 'Alpha')).toBe('Zulu') | Reason no longer applies: this test covered a helper used only by the deleted FeaturePolicyEditor. |
| columnLabel.unit.test.ts :: normalizedColumnLabel > trims a non-empty label and restores the saved label when empty | expect(normalizedColumnLabel(' ', 'Alpha')).toBe('Alpha') | Reason no longer applies: this test covered a helper used only by the deleted FeaturePolicyEditor. |
| columnLabel.unit.test.ts :: normalizedColumnLabel > trims a non-empty label and restores the saved label when empty | expect(normalizedColumnLabel('', 'Alpha')).toBe('Alpha') | Reason no longer applies: this test covered a helper used only by the deleted FeaturePolicyEditor. |
| projectViewDraft.unit.test.ts | moves array item from index 0 to 1 | dropped: moveArrayItem had no production importer, so its test-only helper and behavior were removed |
| projectViewDraft.unit.test.ts | boundary offset preserves the array | dropped: moveArrayItem had no production importer, so its test-only helper and behavior were removed |
| projectViewDraft.unit.test.ts | does not mutate input array | dropped: moveArrayItem had no production importer, so its test-only helper and behavior were removed |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.getAllByText(/Not assessed/)).toHaveLength(2); | Retired panel has no production renderer. Live review retains lossLabel. |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.queryByText(/ML-ready/)).toBeNull(); | Retired panel has no production renderer. Live review retains lossLabel. |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.getByText(/Data quality and feature meaning still need review/)).toBeInTheDocument(); | Retired panel has no production renderer. Live review retains lossLabel. |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.getByText(/Only the first related record is kept/)).toBeInTheDocument(); | Retired panel has no production renderer. Live review retains lossLabel. |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.getByText(/Structure:/).parentElement).toHaveTextContent('Scalar columns'); | Retired panel has no production renderer. Live review retains lossLabel. |
| ui/packages/loom-ui/src/features/ExplorerBuilder/components/DataframeContractPanel.unit.test.tsx: Dataframe contract evidence | expect(screen.getByText(/Lossless:/).parentElement).toHaveTextContent('No'); | Retired panel has no production renderer. Live review retains lossLabel. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(await screen.findByText('A reusable patient code meaning')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(screen.queryByText('Wrong profile')).toBeNull() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(screen.getByText('revision-1')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(await screen.findByText('Sample only: the bounded review did not exhaust the output.')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(screen.getByText('code[1]')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(screen.queryByText('Sample only: the bounded review did not exhaust the output.')).toBeNull() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | expect(applied).toEqual([{ type: 'APPLY_INTERPRETATION_CANDIDATE', outputId: 'patients', column: 'code', interpretationCandidate: { candidateReceiptId: 'candidate-receipt', revisionId: 'revision-1' }, }]) | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > creates from the saved column without sending rules or auto-applying the result | expect(await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > creates from the saved column without sending rules or auto-applying the result | expect(onContextRefresh).toHaveBeenCalledTimes(1) | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > creates from the saved column without sending rules or auto-applying the result | expect(applied).toEqual([]) | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows pinned summaries from context and keeps creation disabled | expect(screen.getByText('Pinned revision revision-pinned')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows pinned summaries from context and keeps creation disabled | expect(screen.getByText('Pinned legacy meaning · authored by researcher')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows pinned summaries from context and keeps creation disabled | expect(screen.getByText('This feature is already pinned. Choose an inline feature to create a mapping from a saved column.')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows pinned summaries from context and keeps creation disabled | expect(screen.getByRole('button', { name: 'Save reusable mapping' })).toBeDisabled() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows unavailable context states without offering a client-selected mapping | expect(screen.getByText('The saved source has multiple matches.')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows unavailable context states without offering a client-selected mapping | expect(screen.getByRole('button', { name: 'Save reusable mapping' })).toBeDisabled() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows loading and context errors explicitly | expect(screen.getByText('Loading saved interpretation context…')).toBeTruthy() | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > shows loading and context errors explicitly | expect(screen.getByRole('alert')).toHaveTextContent('draft changed') | Reason no longer applies: no production render site at HEAD; the assertion covered the removed interpretation editor only. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > renders only server-listed applicable summaries, then reviews, cancels, and applies the exact receipt | await screen.findByText('Compared 1 · Changed 1 · Resolved 1 · Unresolved 0') | Reason no longer applies: no production render site for the removed editor. |
| InterpretationPanel.unit.test.tsx :: InterpretationPanel > revises the selected applicable head with its exact parent revision | await screen.findByText('Saved to the reusable library. The feature remains unchanged until you review and apply it.') | Reason no longer applies: no production render site for the removed editor. |

## Replacement for the UI section of TARGET_TREE.md

```markdown
### UI

Follow [the UI plumbing implementation plan](docs/UI_PLUMBING_IMPLEMENTATION_PLAN.md) for Loom UI source layout. Read `src/contracts/`, `src/client/`, `src/data/`, `src/display/`, and `src/features/` from dependency contracts toward feature entry points.

Builder domain data and transformations live in `src/features/ExplorerBuilder/model/`. Draft state and workflows live in `session/`. Browser observation and document and window subscriptions live in `dom/`. The Builder layout and views live in `ui/`, grouped into catalog, construction, reshape, traversal, row definition, population, review, source rules, preview, toolbar, shared UI, and workspace.

Keep server requests behind client and data bindings. Verify public exports with `public-api.json` and package architecture with `npm --prefix ui run check-boundaries --workspace @calypr/loom-ui`.
```

## Commits

- `5c68879a9` P0: Trace UI dispatches and client request lifetimes in Vitest
- `4a215667a` P0: Record the blocked UI plumbing acceptance gate
- `d4799c609` P0: Verify UI plumbing at behavioral checkpoints
- `a1cc74b64` P1: Remove unreachable UI code and pin the public exports
- `b4a920666` P2: Move wire contracts and isolate the Loom client
- `233585714` P2: Share semantic and construction route identity
- `5b29cf428` P2: Move data primitives, feature shells and display helpers
- `ec95753ea` P3: Put Builder models and their test seams below UI
- `5c5dbdf86` P2: Own query request slots with a reusable cancellation owner
- `fd05e7d82` P4: Give Builder workflows a shared draft accessor
- `0f787b40a` P4: Reuse the draft revision comparison across preview and Apply
- `0203220fa` P4: Own remembered-table persistence beside the Builder draft
- `0fdcb8dd8` P4: Give the Builder session its server draft subscription
- `a1ccf3be3` P4: Share draft hydration across initial load and recovery
- `01fe5cff7` P4: Publish Builder draft edits through the session store
- `251d665c3` P4: Provide draft state and actions through Builder session context
- `d675ebe8d` P4: Own automatic preview caching and reload in the session
- `0c7a14058` P4: Move compile and preview recovery actions into the session
- `2ab050cee` P4: Put command serialization and settlement in the session
- `a4a7942d8` P4: Own automatic preview execution and limit in the session
- `e7eb042c2` P4: Track Builder compile and preview in named request slots
- `a764d4381` P4: Use the shared store for root-change state
- `90536f975` P4: Load construction capabilities through a scoped resource
- `a9f8e80f8` P4: Move row-change assessment and preview execution into the session
- `91c596695` P4: Give construction proposals and discovery their own request slots
- `6b63d10ee` P4: Move construction proposal validation and loading below UI
- `0344db1c5` P4: Bind root-change assessment and preview to one request owner
- `01a2fbdff` P4: Own construction proposal and discovery state in the session store
- `e8b6b0a3e` P4: Run proposal, discovery and Apply actions in the session
- `031522ed2` P4: Bind root-change choices and recovery to the Builder session
- `4b655e07d` P4: Move workspace selection and edit state into the session
- `62694ceef` P4: Own shared workspace state and cancellation in the session
- `22d68cde0` P4: Move first-table progress and creation into the session
- `9ad96eada` P4: Give navigation, source recipe, and traversal named session actions
- `2f6af2345` P4: Own recipe edit tokens and transitions in a plain session controller
- `2863b8ee5` P4: Own add-column and combine requests in session scopes
- `450a892d3` P4: Bind remaining feature workflows to session scope
- `102187622` P4: Move Builder route queries to data ownership
- `212ff56cb` P4: Own preview lineage requests in the session
- `667889d63` P4: Move framing and related-route queries into the session
- `697e51ccd` P4: Scope coded Pivot and contributor requests to their editors
- `4baae7c88` P4: Own reshape inventory and related choice requests in session modules
- `0b7372b19` P4: Own row-definition and explicit group requests in the session
- `9ad8cf952` P4: Move population and interpretation workflows into the session
- `5dd7d2a00` P4: Give source relationship and column policy named request slots
- `5a0ba328e` P4: Scope catalog options and Viewer action requests
- `ed3cbb643` P2: Replace named Builder mutation hooks with typed method bindings
- `c6db68b85` P4: Let the operation editor read shared Builder state from its owners
- `421438d4e` P4: Separate Builder session orchestration from workspace layout
- `1b15f925d` P4: Bind operation editor controls to their session
- `f75ca83ee` P4: Centralize partial draft and catalog comparisons
- `6a08ee3be` P4: Bind row definition controls to the Builder session
- `f5edff61a` P4: Bind Builder views to scoped session values
- `7832351ba` P4: Bind feature catalog to its Builder scope
- `367aac90c` P4: Reuse catalog scope for request inputs
- `c620c85fb` P5: Group Builder views by domain
- `a6f15afd7` P5: Deliver dataset review focus through target refs
- `f7572aa67` P5: Subscribe to table actions position from the anchor
- `39f4d2129` P5: Centralize DOM observers and Escape
- `61b1c3e2e` P2: Retain distinct semantic identity matchers
- `fe23f31ea` P5: Reuse session owners in Builder actions
- `a2cf34edb` P5: Read frame and population scope from the session
