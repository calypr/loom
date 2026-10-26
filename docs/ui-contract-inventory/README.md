# Builder verification checkpoint

This checkpoint retains the source inventory, TLA+ models, UI drivers, and Go lifecycle fixtures for Builder verification. It is based on application commit `dc331783dde3f61e764bab3a421b66b28698c9d4`.

The inventory describes 57 affordances and 159 request checkpoints from source commit `dc331783dde3f61e764bab3a421b66b28698c9d4`. The Pivot discovery and construction lifecycle changes in that commit were reviewed before refreshing the source hashes. Further source changes require a new review; a source-drift failure is expected until the model and hashes are updated deliberately. The fragment files retain their original extraction pins; `ui-supported-affordances.json` is the normalized reviewed manifest.

The integrated TLC runner is:

    node scripts/verify-builder-formal.mjs

It verifies the pinned sources in `builder-preview-contract.json`, runs the bounded `BuilderOperations`, `BuilderPreview`, `WorkspaceLifecycle`, `RowDefinitionLifecycle`, `DraftRestore`, `RootChange`, `ColumnPresentation`, `Population`, `Interpretation`, `SupportingSurfaces`, and `LifecycleEntry` models, regression-tests the fixed Apply-acknowledgment ownership paths, records the retired Pivot missing-category refusal finding, runs the real React hook schedules, validates the captured traces against TLC model paths through `builder-trace-contract.json`, runs focused Go replay tests, and writes `report.json` and `report.md`.

Apply-ack browser acceptance was attempted against an isolated stack built from the current worktree. The existing Basic authoring case stopped before Apply because the test expects a `Fields and related data` control that the current UI does not render. This is a harness/source mismatch, not an Apply-ack product failure; the two formal hook regression schedules remain the ownership evidence.

The Phase 1 trace layer currently records 61 formal traces, 12 unique after deduplication, and validates all 61. It uses `BuilderPreview.tla` for 60 traces and `ConstructionLifecycle.tla` for the A-B-A schedule. The default run is a harness pass: it currently reports zero active findings, one retired Pivot refusal finding, and zero coverage gaps. Use `node scripts/verify-builder-formal.mjs --strict` for the full gate; it currently passes and fails if a retained finding or coverage gap is reintroduced. The report separates:

- design counterexamples and unmet backend support;
- resolved regressions with their regression tests;
- implementation contract failures;
- partially modeled or unmodeled behavior;
- implementation fixture gaps.

The route-level plan is [Builder formal verification plan](../verification/BUILDER_FORMAL_VERIFICATION_PLAN.md).

The coverage map is `affordance-model-map.json`. It currently records all 57 affordances as modeled and implementation-tested, with no fixture gaps or untested entries. This completes the inventory coverage map, not every runtime permutation or backend path.

The abandoned `full-ui` JavaScript state explorer and its comprehensive runner remain excluded. `ConstructionLifecycle.tla` with `verify-builder-interactions.mjs` remains the narrow proposal-ownership check. `verify-builder-operation-history.mjs` remains the bounded Group operation-history and Go replay check. The integrated runner above is the orchestrator.

The TLA+ checks are bounded to their checked-in configs. They do not establish unbounded refinement, full browser lifecycle, server persistence, calculated value correctness, or production transport behavior. The inventory `--check` mode still reports pending per-affordance coverage metadata; the integrated runner uses `--source-only` plus the separate coverage map.

Product code and unrelated worktree changes are outside this checkpoint.
