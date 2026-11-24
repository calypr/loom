# Loom UI plumbing: implementation plan

Date: 2026-11-24. This plan implements `docs/UI_PLUMBING_AUDIT.md` (findings F1–F8). It replaces
the UI section of `TARGET_TREE.md`. Each finding is fixed at its cause, and each fix ships with a
guard that keeps it from coming back.

**Invariant for every phase:** no visible UI change. Rendered DOM, copy, styling, focus, and
interaction stay the same. The public package exports also stay the same, with one exception:
the Builder internals leave the public surface (G1). There are two known consumers:
- The IDP frontend imports about 14 names from the root and `/saved-views` entries.
- Loom's demo app lazily loads `/builder` and `/viewer`.

All four entries stay. The frontend's Jest mocks also pin component names, so a public rename
breaks it even if TypeScript would accept the change.

## Root causes behind the findings

The 8 findings come from 4 causes. The fixes target the causes.

| Cause | Findings | Evidence |
| --- | --- | --- |
| **C1. The Builder session is made of React hooks that depend on each other in a cycle.** Every workflow (draft, command queue, preview, reconcile, proposals, root change, recipe editing, add columns, ...) is a hook. React requires hooks to run in a fixed order, so a hook cannot take a callback from a hook that runs later. The code works around this by splitting each owner into a state hook plus `actionsFor(lateDeps)`, reading fresh state through `latestState` refs, and passing setters and refs through option bags. | F3, F6, part of F5 | Option bags of 16–25 fields. Five `actionsFor` calls in `BuilderWorkspace`. 35 `useState` and 55 `useRef` across the hooks. 3 hand-rolled stores. 57-prop render bags. |
| **C2. Request ownership is re-implemented at every call site.** Each async owner hand-writes "is this response still for the current draft?" and "abort the old request". | F3, F6, F5 | 131 field-by-field `snapshotToken`/`draftVersion`/`draftDigest` comparisons. 27 `AbortController`s in 20 files. 58 epoch-counter references in 7 files. 14 UI components call the server directly. |
| **C3. There is no layer model.** No rule says what may import what, so folders accumulate by habit. Domain identity rules have no home, so `api.ts` and the features each keep their own copy. | F4, F7, F8 | Directories import each other both ways. Pure logic sits in `components/`. `sameConstructionRoute` and semantic identity are implemented twice. There is an `api.ts` <-> `selection.ts` cycle. |
| **C4. There are no guards on reachability, export use, effects, or the public surface.** | F1, F2, F6 (the bypass) | 9 unreachable files, 204 unused exports, and a `useEffect` ratchet of `{}` that `useBrowserSubscription` gets around. The public barrels use `export *` and leak internals. |

## Target architecture

```
src/
  index.ts  viewer-entry.ts  builder-entry.ts  saved-views-entry.ts   entries only, named exports
  contracts/   wire types + zod schemas (types.ts, sourceRecipeParameters, interpretation, selection),
               identity/equality rules (route equality, semantic-item identity), guards (isRecord)
  client/      transport, errors, createLoomClient            imports contracts only
  data/        LoomProvider, useQuery/useKeyedQuery, useMutation, createStore,
               RequestOwner (request slots tied to an identity)  imports contracts, client
  display/     valueDisplay, resultUnitDisplay, cellTrace
  features/
    ExplorerViewer/       Viewer.tsx (moved) + existing
    ExplorerViewBuilder/  projectViewBuilder*.ts, projectViewSelection.ts (moved) + existing
    ExplorerBuilder/
      Builder.tsx         shell (moved from root)
      model/              pure functions and types: reducer, authoring model, catalogItems,
                          populationRoutes, constructionHistory, reshapeForms, rowRecipeProjection,
                          traversalScopeModel, graph layout math, ...
      session/            BuilderSession: one store, slices, actions, request slots, React bindings
      ui/<domain>/        components only: catalog, construction, reshape, traversal,
                          rowDefinition, population, review, sourceRules, preview, toolbar
      dom/                DOM/window subscriptions (escape stack, dismiss, beforeunload, viewport)
```

**Layer rule**, enforced in `check-boundaries.mjs`:

- Imports flow in one direction: `contracts` -> `client` -> `data` -> `display` -> `features`.
- Inside the Builder: `model` -> `session` -> `ui`. `dom` is imported only by `ui`.
- `ui/**` must not import `client/`, `useLoomClient`, or `LoomClient`. This is what enforces the
  F5 fix.
- Features must not import each other except through `contracts`, `data`, and `display`.

**BuilderSession** (C1 and C2 fixed together):

- `createBuilderSession({ client, scope })` returns a plain TypeScript object. Its parts:
  - One store, built with `createStore`, holding: the draft (the existing pure reducer, unchanged
    in `model/`), workspace UI state (selected table, occurrence, active operation family,
    editing step, history selection, column selection, row-intent session, review target,
    advanced view, feature mode, messages, ...), and each workflow's state (command queue,
    preview, reconcile, proposal, root change, recipe editing, add columns, combine catalog,
    population, interpretation context, publish, CRUD).
  - Named actions for every workflow. An action reads the current state from the store, so no
    `latestState` refs are needed. Actions call each other directly, so no `actionsFor` late
    binding is needed.
  - A `RequestOwner` with named slots. `run(slot, identity, load)` aborts the slot's previous
    request. It also aborts every slot whose identity becomes stale when the draft identity
    changes. It applies a result only if the slot and identity are still current.
    `DraftIdentity` (owner key, snapshot token, draft version, draft digest, plus an optional
    output, proposal, or limit) is defined once, with `sameIdentity` and `isCurrent`.
- React bindings: `BuilderSessionProvider`, `useBuilderSession(selector)`, and
  `useBuilderActions()`.
  - The session is created during render, keyed by owner key and client. Creating it has no side
    effects.
  - Loading starts on the first store subscription.
  - Disposal (aborting all slots) runs in a microtask after the last unsubscribe. This is the
    pattern `react.tsx`'s `resourceFor` already uses, and it is safe under StrictMode's double
    subscribe. No `useEffect` is involved.
- Workflow logic can then be tested as plain TypeScript, without rendering anything.

## Phases

Each phase lists its findings, work, guards, exit criteria, and checks. Every phase runs
`make test-ui` and `npm run build`, plus the oracle diff described in Phase 0. Phases 4 and 5
also need `make e2e` and `.codex/skills/verify` Builder acceptance. Each nontrivial unit gets
`pstack:interrogate` before commit, as AGENTS.md requires.

### Phase 0: oracle and guards (C4)

1. **Equivalence oracle.** This is what makes "no UI change" checkable across a large rewire.
   - Turn it on with `LOOM_UI_TRACE=<dir>` in `vitest.setup.ts`.
   - For every test, record the normalized `document.body.innerHTML` after each user-event
     dispatch and at test end. Also record the mock client's call log: method, arguments, and
     whether the call was aborted (from `testSupport/loomClientMock.ts`).
   - Normalize React `useId` values (`:r…:`) and UUID-derived ids by renumbering them in
     first-seen order. Without this, adding the session provider would change ids and show
     false diffs.
   - Write traces to the scratchpad or `.cache/`, never to Git.
   - The 19 workspace journey files (21.6k lines) are the main corpus.
   - DOM diffs must be empty. Call-log diffs must be empty, or each one is explained as removing
     a redundant request that was always aborted or discarded. Record every explained diff in
     the phase's commit message.
2. **Graph audit.** Commit `docs/ui-graph-audit.draft.mjs` as
   `ui/packages/loom-ui/scripts/graph-audit.mjs` and delete the drafts. It reports:
   reachability, unused and test-only exports, cycles, directory edges, layer violations,
   prop-bag widths, and staleness-comparison counts.
3. **Ratchets in `check-boundaries.mjs`.** The audit script feeds these numbers, and the counts
   only go down:
   - unreachable production files
   - unused exports
   - import cycles
   - layer violations (report-only until Phase 5)
   - effect calls, now counting `useBrowserSubscription` and `useCommitLifecycle`
   - `ui`-layer server access (report-only until Phase 4 ends)
4. **Public API manifest.** Generate `ui/packages/loom-ui/public-api.json` (the exported names
   for each of the 4 entries) from the entry modules. `check-boundaries` fails when it drifts.
   From then on, a public surface change is always a reviewed diff.

Exit: the oracle captures a baseline at the base commit, and all guards pass at current counts.

### Phase 1: dead code and export surface (F1, F2, C4)

- Delete: `FeaturePolicyEditor.tsx` and its test, `discovery/ObservedCodeEvidence.tsx`,
  `ColumnSourceInspector.tsx`, `constructionOperations/{index,operationFamilies}.ts`, the
  `ExplorerViewer/index.ts` and `ExplorerViewBuilder/index.ts` barrels, the
  `components/useDismissibleLayer.ts` shim (repoint its 3 importers), `columnLabel.ts` and its
  test, and `useBrowseSemanticInventoryV2Mutation`. `FeaturePolicyEditor` is confirmed dead in
  both Loom and the frontend (G2).
- Move `liveConstructionCapabilitiesFixtures.ts` to `testSupport/`.
- Delete the exports that are dead even inside their own file. Drop `export` from the remaining
  unused exports.
- Replace `export *` in every entry and barrel with explicit named exports.
- Drop the Builder internals (`builderAuthoringReducer`, `authoring/model`, `previewRecovery`,
  `routeActions`) from the public surface (G1). They stay as internal modules because Loom uses
  them.
- Bump `@calypr/loom-ui` by a minor version (0.1.5 -> 0.2.0).
- Write `public-api.json` with the names the frontend and the demo actually consume, and
  record that consumer list in a header field.
- Flip the ratchets to zero: no unreachable production files and no unused exports.

Exit: the graph audit reports 0 unreachable, 0 unused, and the oracle shows no diff. Expected
change is about -2,800 lines.

### Phase 2: foundations (F7, the cycle, the `data/` half of F5, C2 primitives)

- Create `contracts/`:
  - Move `types.ts`, `sourceRecipeParameters.ts`, the schemas from `interpretation.ts`, and the
    types from `selection.ts`.
  - Add `contracts/identity.ts`, holding the single implementations of `sameConstructionRoute`,
    semantic-item identity, and `sameSemanticItemIdentity`.
  - Add `contracts/guards.ts` for `isRecord`.
  - Before merging the two semantic-identity implementations (`api.ts:1101/1117` and
    `catalogItems.ts:99`), add a table test that runs both over every identity kind, plus the
    edge cases (`selectionMode` set without `familyId`, and the reverse). Merge only if the
    outputs match. Otherwise stop and report the divergence, because one of them is a bug.
- Create `client/` from `api.ts`. `client/` imports `contracts/` only, which breaks the
  `api.ts` <-> `selection.ts` cycle.
- Create `data/`:
  - Move `LoomProvider`, `useQuery`/`useKeyedQuery`, and `resourceFor` here.
  - Add one generic `useMutation`, which replaces the copies in `react.tsx` and
    `projectViewBuilderReact.tsx`.
  - Add `createStore<T>`.
  - Add `RequestOwner` and `DraftIdentity`, with unit tests for: supersede aborts the old
    request; an identity change aborts stale slots; a late result after supersede is dropped; a
    rejection after abort is swallowed; disposal aborts everything.
  - Optional: write a pstack statechart model of the slot/identity invariant ("no result from a
    stale identity is ever applied"), since this primitive replaces 131 hand-written checks.
- Delete the 11 RTK-style `use*Mutation` wrappers. The Viewer and the saved-views builder call
  `useMutation((args, signal) => client.x(args, signal))` at their call sites. The Builder call
  sites move to session actions in Phase 4, so in Phase 2 they switch to the generic hook as a
  stepping stone.
- Move `Builder.tsx`, `Viewer.tsx`, and the `projectView*` files into their features. Move the
  display helpers into `display/`. The entries re-export them, so `public-api.json` does not
  change.
- Replace the duplicate `newId` and `choicesMatchRequest` copies with shared helpers. Keep
  `errorMessage` at each call site, since the fallback wording differs.

Exit: 0 cycles, `contracts`/`client`/`data` layer violations at 0, oracle shows no diff, and
`public-api.json` is unchanged.

### Phase 3: move pure model code (the F4 prerequisite)

Moves only, with no logic edits.

- Move pure modules into `ExplorerBuilder/model/`: `authoring/{model,reducer,previewRecovery,
  routeActions,rootChangeSelection,admission,...}`, `catalogItems`, `populationRoutes`,
  `columnRuleInventory`, `sourceColumnRule`, `constructionWorkspace/{constructionHistory,
  constructionStages,outputEligibility,sourceProjectionAvailability,appliedChoicePreview,...}`,
  `constructionOperations/{reshapeForms,editorShared,existingFieldReuse,...}`, and the pure
  files in `components/` (`rowRecipeProjection`, `traversalScopeModel`, `authoredTraversal`,
  `virtualization`, `datasetReviewSelection`).
- Pure helpers that live inside components and are exported only for tests (`layoutTraversalTree`,
  `layoutDiscoveryRail`, `graphEdgeOverviewLabels`, `replacementConsumerPresentations`,
  `previewValueCoverage`, ...) move to `model/` too.
- New rule: test-only exports are allowed only from `model/` and `session/`.
- Hooks and components stay where they are. Phase 4 deletes or rewrites most of them, so moving
  them now would only create churn.

Exit: `model/` imports nothing from the Builder except `model/`, plus `contracts` and `display`.
The oracle shows no diff.

### Phase 4: BuilderSession (F3; the Builder half of F5; the request half of F6; C1 and C2)

This phase is serial: `BuilderWorkspace.tsx` has a single writer. Each step absorbs owners into
the session, then in the same commit deletes the hook, its `actionsFor`, its option bag, and the
props that carried it. Each step ports that hook's white-box tests (11 files, 10 of them
`renderHook` tests) to plain session tests. The 19 journey test files must pass unchanged. Steps
run leaves first:

1. **Session skeleton and draft.**
   - `createBuilderSession`, the provider, and the selector hooks.
   - The draft slice wraps the existing reducer.
   - Server load and hydrate replace `useBuilderDraft` and `useGetExplorerBuilderStateV2Query`.
   - Remembered-table sessionStorage moves into the session.
   - Every `latestState` ref becomes `session.getState()`.
2. **The async core.** `useAutomaticPreview`, `useAutomaticPreviewOwner`, `useReconcile`, and
   `useCommandQueue` become session actions that run in `RequestOwner` slots. The staleness
   predicates in `useCommandQueue` (`acceptedConstructionForApply`) and
   `useAutomaticPreviewOwner` collapse onto `DraftIdentity`.
3. **Proposal and root change.** `useConstructionLifecycle` and `useRootChange` become slices.
   This deletes 2 of the 3 hand-rolled stores and their epoch counters.
4. **Workspace UI state.**
   - The selection-owner store (the third hand-rolled store) moves into the session, along with
     the 17 `useState` in `BuilderWorkspace`.
   - So do `useConstructionNavigation`, `useRecipeFocus`, `useFirstTable`, `useSourceRecipe`,
     `useTraversalProjectedFields`, and `recipeEditingOwner`.
   - `occurrencePanels`, `cancelingConstructionProposalRef`, and the other ref channels become
     session state.
5. **Feature workflows.** `useAddColumns` (its abort-on-key-change subscription goes away),
   `useCombineCatalog`, `usePopulationExclusion`, `usePublish`, `useTableExplorerCrud`,
   `useSuggestions`, `useReshapeAvailability`, and the interpretation-context and
   population-selection queries.
6. **Component requests.** The 14 UI files that call the server move their calls into session
   slots. Their local `AbortController`s and epochs go away, as do the abort-on-key-change
   subscriptions in `PreviewTable` and `RowDefinitionSettingsPanel`. Files: `GroupCodedValuePicker`,
   `RowDefinitionSettingsPanel`, `RelatedFieldEditor`, `CodedPivotEditor`, `FrameSourcePanel`,
   `RelatedExpand{,Contributor}Editor`, `RelatedEligibilityEditor`, `PopulationMemberRemoval`,
   `InterpretationPanel`, `ExplicitGroupAuthoring`, `ConceptCatalog`, `ConstructionReshapeEditor`,
   `SourceRelationshipReview`, `PopulationPanel`, plus `columnRuleInventory` and
   `recipeNetwork`. After this step, the `ui` server-access ratchet is enforced at 0.
7. **Collapse the prop bags.**
   - `OperationEditorHost` (57 props), `RowDefinitionSettingsPanel` (47), `ImportedSourceSteps`
     (36), `ConstructionWorkspace` (32), `ConceptCatalog` (31), `AuthoredTraversalEditor` (28),
     `TraversalScopeNavigator` (26), and `BuilderToolbar` (22) all read the session through
     selectors.
   - Props stay only for presentation and for values that a parent instance genuinely
     configures.
   - Components that only forwarded state, `OperationEditorHost` in particular, either dissolve
     or become plain layout.

Exit:
- `BuilderWorkspace` is a layout shell.
- Zero `actionsFor`, zero `latestState`, zero hand-rolled stores outside `createStore`.
- No hook takes more than about 8 options.
- No component takes more than about 10 non-presentational props. The graph audit reports this
  and the ratchet enforces it.
- Staleness comparisons outside `contracts/identity`, `data/requests`, and `model/` drop to 0
  (from 131).
- `AbortController` appears only in `data/` and `client/`.
- Full oracle diff is empty, or every diff is explained. `make e2e` and Builder acceptance pass.

### Phase 5: UI layout and honest effects (the rest of F4 and F6)

- Group components into `ui/<domain>/` (catalog, construction, reshape, traversal,
  rowDefinition, population, review, sourceRules, preview, toolbar). Move the DOM hooks into
  `dom/`. Splitting `ConceptCatalog.tsx` and the reshape editors happens here, along the
  ownership the session established, and only where a file still holds more than one owner.
- Switch the layer rule from report-only to enforcing.
- Rename `useBrowserSubscription` to `useDomSubscription`, delete the `useCommitLifecycle`
  alias, and allow it only in `dom/`. The effect ratchet counts its call sites with an
  allowlist of exactly the DOM subscriptions.
- Focus after render (`BuilderWorkspace.tsx:944`): the review-focus action records a pending
  focus token. The target element's ref callback consumes the token and focuses itself. Then
  the timer subscription is deleted.
- Popover position (`ConstructionWorkspace.tsx:188`): turn it into a real external store in
  `dom/` whose snapshot is computed from the anchor rect and updated on scroll and resize, read
  with `useSyncExternalStore`. This removes `setState` inside `subscribe`.
- Escape ownership: `useDismissibleLayer` registers with the same Escape handler stack as
  `useEscapeToClose`, so there is one owner. First add a test that pins the current
  nested-layer Escape order, and keep that order exactly.
- Update `TARGET_TREE.md`'s UI section to point at this plan and remove its superseded bullets.

Exit: all ratchets at their final values and enforcing, the oracle shows no diff, and the full
acceptance run passes on the final snapshot.

## Gates

G1, G2, and G4 are resolved. G3 stays open for the whole run.

- **G4, session-store direction: approved 2026-11-24.** Execution started on branch
  `ui-plumbing-refactor` from `main` at `f19a60057`.

- **G1, public surface (Phase 1): resolved 2026-11-24.** I searched the
  `~/Desktop/FFNEW/IDP-Frontend` branches that import `@calypr/loom-ui`
  (`feature/clickhouse-explorer` 2026-11-20, `fix/loom-portable-saved-views`,
  `feature/audit-optim`, `feature/manual-audit-refresh`). Between them they import these names:
  - from `@calypr/loom-ui`: `createLoomClient`, `LoomClient`, `LoomExplorerBuilder`
  - from `/saved-views`: `LoomSavedViewBuilder`, `LoomSavedViewBuilderProps`,
    `getProjectViewBuilderState`, `projectViewBuilderPath`, `selectedProjectViewId`,
    `selectedProjectViewTab`, `useProjectViewTab`, and the four `*ProjectView*Args` types
  - also the `styles.css` subpath, mocked in Jest tests.

  No branch imports a Builder internal, and nothing imports `/builder` or `/viewer`. The
  internals are heavily used inside Loom (`authoring/model` by 43 production files, `reducer` by
  13), so nothing moves to the frontend. They stop being public, which takes a minor bump while
  the package is 0.x. `public-api.json` records the consumed set above as the frontend
  contract, and the demo app's imports stay supported.
- **G2, `FeaturePolicyEditor` (Phase 1): resolved 2026-11-24.** It is referenced on none of the
  40 most recent IDP-Frontend branches and has no production importer in Loom, so it gets
  deleted. Git can restore it.
- **G3, oracle diffs (Phases 2–5):** You approve any explained call-log diff before it is
  committed. Any DOM diff is a defect, never a judgment call.

## Parallelism and cost

- Phase 0 has two independent tracks: the oracle and the guards/manifest.
- Phase 1 deletions and Phase 2 sub-moves (contracts, client, data, display) can run as
  independent worktree units, merged in order.
- Phase 3 is mechanical.
- Phase 4 is serial by design, because one writer owns `BuilderWorkspace` and the session. A
  parallel reviewer can check each step's interrogate while the next step is being written.
- Phase 5's domain moves can fan out once the layer rule is in report mode.

Size estimates:

| Phase | Estimate |
| --- | --- |
| 0 | small |
| 1 | about -2.8k lines |
| 2 | medium |
| 3 | mechanical |
| 4 | the bulk: about 9k lines of hooks, workspace, and prop plumbing rewritten into a smaller session, with net deletion expected |
| 5 | medium |

## Not in scope

- Any visual, copy, or interaction change.
- Splitting `types.ts` by domain. It moves whole into `contracts/`; splitting it is a separate
  readability task.
- Rewriting the existing white-box tests outside the hooks that Phase 4 replaces.
- Backend or OpenAPI changes.
