# Loom UI plumbing audit

Date: 2026-11-24. Scope: `ui/packages/loom-ui/src`. This audit covers dead code and how the JS
is composed. No visible UI change is in scope: rendered DOM, copy, styling, and interaction
behavior stay identical. The public package surface (`index.ts`, `viewer-entry.ts`,
`builder-entry.ts`, `saved-views-entry.ts`) stays source-compatible, except that the Builder internals leave the public surface (Q1,
resolved).

This builds on the UI section of `TARGET_TREE.md`, which proposes file splits. This audit argues
that the Builder's state ownership has to change first. Splitting `BuilderWorkspace.tsx` before
that change would only spread its 57-prop and 25-option bags across more files.

## Measured baseline

Source: TypeScript-AST import graph from the four package entry points, plus `vi.mock` and
dynamic imports. The script is in the scratchpad (see "Reproduce").

| Metric | Value |
| --- | --- |
| Production / test lines | 50,751 / 56,782 |
| Non-test files unreachable from any entry | 9 (4 test-only, 5 orphaned) |
| Exports with no importer | 204 |
| Exports imported only by tests | 67 |
| Production import cycles | 1 (`api.ts` <-> `selection.ts`) |
| `BuilderWorkspace.tsx` | 2,370 lines, 70 imports, 17 `useState`, 7 `useRef` |
| Widest prop bag | `<OperationEditorHost>`: 57 props |
| `useEffect` baseline | `{}`, while 9 sites use `useBrowserSubscription` as an effect |

## Findings

### F1. Dead production files (about 2,750 lines)

| File | Lines | Evidence |
| --- | --- | --- |
| `components/FeaturePolicyEditor.tsx` + its test | 1,501 + 844 | Only its own test reaches it. No entry imports it. |
| `discovery/ObservedCodeEvidence.tsx` | 58 | Only `FeaturePolicyEditor` imports it. |
| `components/ColumnSourceInspector.tsx` | 99 | Nothing imports it. Last touched 2026-09-20. |
| `constructionOperations/{index,operationFamilies}.ts` | 61 | Orphaned barrel and its only target. |
| `ExplorerViewer/index.ts`, `ExplorerViewBuilder/index.ts` | 7 | Orphaned barrels. Entry points import the files directly. |
| `components/useDismissibleLayer.ts` | 1 | Re-export shim of `hooks/useDismissibleLayer`. It has 3 importers to repoint. |
| `components/columnLabel.ts` | 4 | Only its test imports it. |
| `react.tsx: useBrowseSemanticInventoryV2Mutation` | 4 | Only tests use it. Production calls `client.browseSemanticInventory` 8 times. |

Also, `constructionOperations/liveConstructionCapabilitiesFixtures.ts` (1,135 lines) is a test
fixture inside `src/`. Move it to `testSupport/`; it should not be deleted.

`TARGET_TREE.md` also lists `datasetReviewSelection.ts` as dead. It is reachable from
production now, so it stays.

### F2. Export surface noise

There are 204 exports with no importer. Most are types and schemas that their own module uses
internally, such as 28 `*Args` types in `api.ts`, 11 sub-schemas in `interpretation.ts`, and
about 30 in `reshapeForms.ts`. For those, the fix is to drop `export`. Some also have no use
inside their own file, so they are dead code: `rowRecipeSentence`, `authoredRowMeaning`,
`directCatalogChoiceIntents`, `rootFieldCatalogItems`, `IconGripVertical`,
`TraversalSourceField`, `PROJECT_VIEW_CHART_TYPES`, and `RelatedFeatureCreator`.

The 67 test-only exports exist so tests can reach internals directly (white-box tests). This
audit leaves them alone. They become relevant only when a move breaks them.

The public barrel `export * from './features/ExplorerBuilder'` also publishes internals:
`builderAuthoringReducer`, all of `authoring/model`, `previewRecovery`, and `routeActions`
(see Q1).

### F3. Builder state has no single owner (the main problem)

`BuilderWorkspaceContent` is a 1,600-line hook body followed by 500 lines of JSX. Some of its
hooks were pulled out into `authoring/use*.ts`, but by line range rather than by ownership.
They wire into each other through the parent:

- Hook option bags: `useCommandQueue` takes 25 options, `useAddColumns` 24,
  `ConstructionNavigationActions` 24, `useTableExplorerCrud` 22, `TraversalActions` 22,
  `SourceRecipeActions` 20, and `usePublish` and `useRootChange` 16 each. Most fields are
  another hook's setters, refs, or callbacks.
- State is scattered. There is a draft reducer (`useBuilderDraft`), 17 `useState` in the
  workspace, 35 more `useState` and 55 `useRef` across the authoring hooks plus
  `useConstructionLifecycle`, and 3 hand-rolled
  `useSyncExternalStore` stores: the selection owner in `BuilderWorkspace.tsx:183`, the
  root-change store in `useRootChange.ts:91`, and the proposal store in
  `useConstructionLifecycle.ts:197`. `recipeEditingOwner` is a fourth owner.
- Refs carry state between hooks. For example, `cancelingConstructionProposalRef` appears 8 times
  across `BuilderWorkspace.tsx` and `useAddColumns.ts`, and `latestState` is passed into hooks so they can read fresh state.
- Prop bags at render time: `OperationEditorHost` takes 57 props, `RowDefinitionSettingsPanel`
  47, `ImportedSourceSteps` 36, `ConstructionWorkspace` 32, `ConceptCatalog` 31 (passed through
  `OperationEditorHost`), and `AuthoredTraversalEditor` 28.

### F4. Folder layout does not express layers

Cross-directory production import edges inside `ExplorerBuilder` run in both directions between
almost every pair. Examples: `components -> authoring` 15 and `authoring -> components` 1;
`authoring -> constructionWorkspace` 18 and back 2; `constructionOperations -> components` 11;
`components -> constructionWorkspace` 10 and back 6; `authoring -> EB root` 8. `components/`
also holds pure model modules (`rowRecipeProjection.ts`, `traversalScopeModel.ts`,
`authoredTraversal.ts`, `virtualization.ts`, `datasetReviewSelection.ts`). No rule stops a
model module from importing a component.

### F5. Two ways to call the API

`react.tsx` has 11 `use*Mutation` wrappers. Each is used at exactly one call site; one is used
nowhere. About 60 other call sites call `loomClient.*` directly. `projectViewBuilderReact.tsx`
re-implements `useMutation`. None of these mutation hooks are public.

### F6. `useBrowserSubscription` is an effect that bypasses the ratchet

`hooks/useBrowserSubscription.ts` runs arbitrary side effects through `useSyncExternalStore`'s
`subscribe`. Because of that, the `useEffect` baseline of `{}` does not mean there are no
effects. It is also exported under a second name, `useCommitLifecycle`. The 9 sites fall into
two groups:

- **Genuine external subscriptions (keep):** `useDirtyBeforeUnload`, `useEscapeToClose`,
  `useDismissibleLayer`, `useGraphViewportFitter`.
- **Effect-shaped synchronization (repair):**
  - Abort-on-key-change: `useAddColumns.ts:90`, `PreviewTable.tsx:110`, and
    `RowDefinitionSettingsPanel.tsx:530`. The owning action, or a keyed resource like
    `useQuery`, should own request lifetime.
  - Focus after render: `BuilderWorkspace.tsx:944`.
  - `setState` inside subscribe: `ConstructionWorkspace.tsx:188`.

`useDismissibleLayer` also handles Escape itself, bypassing `useEscapeToClose`'s handler stack.
That is two Escape owners. Unifying them could change which layer closes, so it is
behavior-adjacent and needs a test before it changes.

### F7. Duplicated helpers

- `sameConstructionRoute` exists twice, identically: in `api.ts:1085` and `populationRoutes.ts:66`.
- Semantic-item identity is implemented twice: `api.ts:1101/1117` and `catalogItems.ts:99`.
  These must be proven equivalent before merging.
- `isRecord` exists 3 times, `useMutation` 2, `newId` 2, `choicesMatchRequest` 2, and
  `errorMessage` 4 (the fallbacks differ, so keep the wording at each call site).
- `api.ts` does domain matching while validating responses. That is why it owns private copies
  of these helpers.

### F8. Feature shells live at `src/` root

`Builder.tsx`, `Viewer.tsx`, `projectViewBuilder.ts`, `projectViewBuilderReact.tsx`, and
`projectViewSelection.ts` belong to features, but the feature code is under `features/*`. Root
should hold only entries, the client, contracts, and shared display helpers.

## Plan

Every wave is plumbing only. The acceptance bar is the same rendered UI and the same public
exports. Each wave ends with `make test-ui` (boundaries, test typecheck, vitest) and
`npm run build`. Waves 3 and 4 also need `make e2e` / `.codex/skills/verify` Builder acceptance,
because they rewire the live Builder.

**Wave 0: tooling.** Add the graph scan as `ui/packages/loom-ui/scripts/graph-audit.mjs`, run
on demand. Extend `check-boundaries.mjs` in three ways:
- Count `useBrowserSubscription` call sites in the effect ratchet.
- Forbid production files that no entry reaches.
- Once Wave 3 lands, forbid upward imports (`model` must not import `session` or `ui`;
  `session` must not import `ui`).

**Wave 1: delete dead code (F1, F2).** Delete the F1 files, move the fixture to `testSupport/`,
and repoint the 3 `useDismissibleLayer` imports. Delete the no-local-use dead exports and drop
`export` from the rest of the 204. Expected: about -2,800 lines and no runtime change.

**Wave 2: consolidate small plumbing (F5, F7, cycle).**
- Add one `createStore<T>()` utility to replace the 3 hand-rolled stores.
- Add one generic `useMutation` and delete the 11 single-use wrappers and the
  `projectViewBuilderReact` copy.
- Move the shared helpers (`isRecord`, route equality, semantic identity) into
  `contracts/`-level modules that both `api.ts` and features import.
- Break the `api.ts` <-> `selection.ts` cycle.

**Wave 3: move files into layers (F4, F8), moves only.**
- Move the feature shells into `features/*`. Entries keep their exports.
- Inside `ExplorerBuilder`:
  - `model/`: pure functions and types (`authoring/model`, the reducer, `catalogItems`,
    `populationRoutes`, `constructionHistory`, `reshapeForms`, and the pure files now in
    `components/`).
  - `session/`: owners and hooks (`authoring/use*`, `useConstructionLifecycle`,
    `recipeEditingOwner`).
  - `ui/`: panels and editors, grouped by domain as in `TARGET_TREE.md`.
  - `dom/`: the 8 DOM hooks.
- Turn on the layer rule. This replaces the matching `TARGET_TREE.md` bullets.

**Wave 4: a single Builder session owner (F3), done incrementally.**
- Introduce one session store: the draft plus workspace UI state (selected table, active
  family, editing step, history selection, column selection, row-intent session, proposal
  state). It exposes named actions, is provided by context, and is read with
  `useBuilderSession(selector)`.
- Convert the `authoring/use*` hooks from option-bag hooks into action modules over the
  session.
- Migrate one consumer per step, in this order: `OperationEditorHost` (57 props),
  `RowDefinitionSettingsPanel`, `ImportedSourceSteps`, `ConstructionWorkspace`, then the
  toolbar.
- Done means `BuilderWorkspace` is a layout shell, and no panel takes more than about 10 props
  that aren't presentation.
- Only after that, split `BuilderWorkspace.tsx` / `ConceptCatalog.tsx` along the new ownership.
  Each step needs `pstack:interrogate` and Builder acceptance.

**Wave 5: make effects honest (F6).**
- Rename the hook to `useExternalSubscription`, drop the `useCommitLifecycle` alias, and limit
  it to DOM/window subscriptions.
- Move abort-on-key-change into the session actions from Wave 4.
- Replace the focus-after-render and `setState`-in-subscribe sites with ref-callback or
  action-owned logic.
- Add a test before touching the two Escape owners.

## Questions before execution

These are tracked as gates in `docs/UI_PLUMBING_IMPLEMENTATION_PLAN.md`.

- **Q1, public surface: resolved.** No IDP-Frontend branch imports the Builder internals. They
  leave the public surface (plan G1).
- **Q2, `FeaturePolicyEditor`: resolved, dead.** No IDP-Frontend branch or Loom production
  file references it (plan G2).
- **Q3, session store: approved** (plan G4).

## Reproduce

`node docs/ui-graph-audit.draft.mjs ui/packages/loom-ui`. Its output is not committed; rerun
the script to regenerate it. Phase 0 of the plan moves the script into
`ui/packages/loom-ui/scripts/graph-audit.mjs` and deletes the draft.
