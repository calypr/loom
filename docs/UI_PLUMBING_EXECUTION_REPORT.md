# UI plumbing execution report

The refactor stopped at the Phase 0 equivalence gate. Two complete runs of the same commit passed all 1,600 tests and produced different settled request counts and rendered DOM. The local branch contains the test tracing hook. The production source, demo, journey tests, and package version match BASE.

## Source and ledger

BASE is `468fa27eae1d3a9d0f58fb237407d048c4173f1d`. The isolated tracing commit is `5c68879a9940c9fd5daf7c28293d2c8d5a34e126`. The two official captures in `.cache/uiplumb/oracle/base/` and `base-repeat/` used this commit with the dependencies from `ui/package-lock.json`. The full command, source, exit status, duration, deadline, and log for each acceptance run are in `.cache/uiplumb/runs.tsv`. The SHA-256 inventory for the original base traces is in `.cache/uiplumb/base-oracle-sha256.tsv`.

| Phase | Done | Obsolete | Deferred |
| --- | ---: | ---: | ---: |
| 0 | 1 | 0 | 4 |
| 1 | 0 | 0 | 5 |
| 2 | 0 | 0 | 8 |
| 3 | 0 | 0 | 2 |
| 4 | 0 | 0 | 7 |
| 5 | 0 | 0 | 8 |
| Total | 1 | 0 | 34 |

P0.1.1 records the standalone tracing commit that the plan requires before capturing the baseline. Its parent P0.1 records the unsuccessful stability check. The ledger has 34 original rows and one sub-row.

## What the oracle observed

Each official run produced 1,600 distinct test traces and 3,709 DOM samples. The base run recorded 1,749 calls. The comparison found 63 differences in DOM/event records and two tests with call-log differences. These counts describe differences between runs of the **same source**.

- In `BuilderWorkspace.addColumnsSource.unit.test.tsx`, “creates a root table and explains when the catalog has no executable direct ID” recorded three settled calls after the click in BASE. The repeat recorded two. A second `describeAuthoringNetwork` invocation disappeared.
- In `populationExclusion.schedules.unit.test.tsx`, “unrelated save refreshes compare-and-…” recorded 17 calls in BASE and 15 in the repeat. The capabilities, network, and population route calls after step 5 differed. The callbacks received actual requests with draft identities.
- In `exportPopulation.unit.test.tsx`, “scopes pending state to the action run and not to the output currently shown” captured the Mantine button loader at opacity 1 in one trace and 0 in the other. Its transform changed too. The publication acknowledgement journey captured a React Flow SVG pattern with finite geometry in one run and `NaN` in another. These are actual DOM attribute values.
- Some differences were generated identifiers. Mantine uses nine random base-36 characters for control IDs. React IDs also appear in scoped CSS selectors, and generated UUIDs appear in option values and data attributes. The identifier correction was tested separately from the timing experiments.

The request variance has a production call site. `BuilderWorkspace.tsx` attaches an inline ref that invokes `loadRecipeNetwork` after React commits. The network owner includes the serialized workspace via `builderDataKey`. The transport arguments omit that workspace key, so separate renders can issue identical-looking requests. The protected test waits for its own assertions and can finish at different points in that work.

Two full-suite captures, a pair of single-worker runs of five files (216 tests each), and isolated clock experiments distinguished identifier differences from scheduling. The single-worker five-file comparison still found 24 differences. In a disposable probe, draining animation frames to quiescence failed after 100 frames because React Flow continued scheduling them. Advancing exactly one frame per dispatch reproduced a pair of matching focused traces, but changed the captured geometry relative to BASE. A virtual timer probe also matched itself twice for three tests while changing the population journey from 17 calls to 15, omitting settled network calls. Those experiments ran only in isolated copies.

Two complete one-worker captures with expanded identifier normalization passed all 1,600 tests each. Their comparison found 26 DOM/event differences and two request-lifecycle differences. In the Combine edit journey a network promise settled at step 1 in one run and step 9 in the other. The loader also differed between opacity 0 and 1 when both runs used one worker.

The final isolated Phase 0 candidate tracked UUIDs returned by the actual generator, normalized their references in attributes and text, matched React IDs in CSS selectors, and normalized Mantine IDs in event targets and focus paths. It included the graph guards and manifest. Two full runs of that exact candidate passed all 1,600 tests each. Across 3,709 DOM samples, **nine tests still had genuine DOM differences**. Seven involved loader or modal transition states, and two involved SVG pattern geometry. The 1,749 calls matched in this pair. The original pair's settled-call discrepancies remain in its immutable evidence. See `.cache/uiplumb/logs/guard-retry-diff.log` and `.cache/uiplumb/logs/normalized-serial-diff.log`.

**The run accepted zero G3 removals.** The two settled-call discrepancies above are failures. The G3 ledger contains its header only. The test-port ledger also contains its header only. No tests or assertions were deleted, replaced, ported, or dropped.

## Package measurements

| Measurement | BASE | Final code |
| --- | ---: | ---: |
| Production TypeScript/TSX physical lines in `src/`, excluding declarations | 50,738 | 50,738 |
| Test physical lines in `src/` | 56,782 | 56,782 |
| `BuilderWorkspace.tsx` physical lines | 2,369 | 2,369 |
| Unreachable production files | 9 | 9 |
| Unused exports, original scanner | 204 | 204 |
| Test-only exports, original scanner | 67 | 67 |
| Unused / test-only exports including `testSupport/` importers | 203 / 68 | 203 / 68 |
| Import cycles | 1 | 1 |
| Recognized target-layer violations in the candidate scanner, report mode | 0 | 0 |
| Identity comparison lines, audit definition | 131 | 131 |
| Structural identity comparisons in candidate scanner | 153 | 153 |
| `new AbortController` sites | 27 | 27 |
| Builder client-access sites in the candidate scanner | 34 | 34 |
| `useBrowserSubscription` / `useCommitLifecycle` calls | 9 / 7 | 9 / 7 |
| Direct React effect baseline | `{}` | `{}` |

The published audit's 50,751 production lines include the 13 lines in `test-matchers.d.ts`. Its 2,370-line workspace count includes the empty segment after the final newline. The import cycle connects `api.ts` and `selection.ts`. Including test support reclassifies `DescribeAuthoringNetworkArgs`, which `ordinaryNetworkFixture.ts` imports. The target-layer directories have yet to be introduced, and the layer rule is unenforced. The workspace still receives four hooks’ `actionsFor` callbacks. Fifteen production files refer to `latestState`. The selection, proposal, and root-change stores remain separate.

| Widest prop bags | BASE and final | Widest option bags | BASE and final |
| --- | ---: | --- | ---: |
| OperationEditorHost | 57 | useCommandQueue | 25 |
| RowDefinitionSettingsPanel | 47 | actionsFor | 24 |
| ImportedSourceSteps | 37 | useAddColumns | 24 |
| ConceptCatalogContent | 35 | useTableExplorerCrud | 22 |
| GuidedGraphWorkspace | 33 | usePublish | 16 |
| ConstructionWorkspace | 32 | useRootChange | 16 |
| ConceptCatalog | 31 | usePopulationExclusion | 11 |
| AuthoredTraversalEditor | 28 | useReshapeAvailability | 11 |

These are measured widths before classifying presentation props. No width exceptions were accepted. The original graph output is in `.cache/uiplumb/rescan/base.txt`. The extended scanner's measurements and site lists are in `.cache/uiplumb/rescan/at-hook.json`.

## Deferred work and decisions

P0.1 failed the required same-source repeat. P0.2 through P5.effect-rename share that prerequisite. Each row below records the state found on retry. P5.target-tree has a separate ownership restriction.

| Row | State at retry |
| --- | --- |
| `P0.1` | Nine genuine DOM differences persisted after identifier normalization. |
| `P0.2` | The promoted scanner is ready in the isolated candidate. |
| `P0.3` | The candidate guard rejected an exceeded export ratchet. Its full oracle comparison failed. |
| `P0.4` | The candidate manifest covers all four entries and rejected packaging and export drift. |
| `P1.delete` | Nine production files remain unreachable, including `FeaturePolicyEditor.tsx`. |
| `P1.fixture` | `liveConstructionCapabilitiesFixtures.ts` remains under `constructionOperations/`. |
| `P1.exports` | The extended scanner found 203 unused exports. |
| `P1.public-surface` | The package is at 0.1.5 and exports all 23 G1 names on both affected entries. |
| `P1.ratchets` | Both the reachability and unused-export counts exceed zero. |
| `P2.contracts` | `types.ts` and `selection.ts` remain at the source root. |
| `P2.identity-merge` | The API and catalog identity implementations remain separate. The table test awaits Phase 2. |
| `P2.client` | `api.ts` and `selection.ts` still form an import cycle. |
| `P2.data` | `LoomProvider` and `resourceFor` remain in `react.tsx`. |
| `P2.request-owner` | The data layer still lacks the shared request owner. |
| `P2.mutations` | The named mutation wrappers remain, including `useApplyExplorerBuilderCommandsV2Mutation`. |
| `P2.shells` | The Builder, Viewer and project-view shells remain at the source root. |
| `P2.helpers` | Multiple modules define `newId` and `choicesMatchRequest`. |
| `P3.model` | `authoring/model.ts` remains outside the proposed model directory. |
| `P3.test-only-exports` | The extended scanner found 68 test-only exports. The proposed export restriction is absent. |
| `P4.1` | The Builder still uses `useBuilderDraft` and `latestState`. |
| `P4.2` | Preview, reconcile, and the command queue remain hooks. |
| `P4.3` | Construction proposal and root-change hooks retain their own stores. |
| `P4.4` | `BuilderWorkspace` retains its selection store. |
| `P4.5` | The add-columns, publish, and table CRUD workflows remain hooks. |
| `P4.6` | The extended scanner reports 34 Builder client-access sites. |
| `P4.7` | `OperationEditorHost` receives 57 props. The workspace has 2,369 lines. |
| `P5.ui-domains` | Builder components remain in their existing directories. |
| `P5.dom` | Builder browser hooks remain in `hooks/`. |
| `P5.layer-enforce` | The target-layer rule is unenforced. |
| `P5.focus` | The workspace retains the review-focus subscription. |
| `P5.position` | `ConstructionWorkspace` retains the position subscription. |
| `P5.escape` | The two Escape handlers remain. |
| `P5.effect-rename` | The two effect-like subscription names have 16 call sites. |
| `P5.target-tree` | Operator-owned untracked file. Both status checks found `?? TARGET_TREE.md`. |

The combined scanner, ratchets, manifest, and identifier correction are available as an uncommitted candidate under `.cache/uiplumb/worktrees/phase0-candidate/`. Its ordinary checks distinguish a tooling failure from the stability failure. The boundary check enforces the existing effect baseline, graph counts, and four entry exports. Isolated probes rejected an exceeded export ratchet, a changed manifest name, a missing stylesheet, and a swapped Builder/Viewer mapping.

The stability gate applies to every implementation unit, including moves and deletions. The single-worker retry above rechecked that common prerequisite. Each row's individual scope and retry disposition is recorded in `.cache/uiplumb/ledger.tsv`. A stable replay of the original asynchronous schedule that reproduces its DOM and requests would unblock those units. A different equivalence contract or a new starting revision with independently repaired scheduling would require an operator decision.

- The live checkout had an existing `@hello-pangea/dnd` symlink to another React installation. Six saved-view tests failed there. A clean, offline lockfile install in an isolated checkout passed all seven tests in that file and the complete suite. The build and live checkout's dependency tree were kept distinct for verification.
- Full checks ran with bounded deadlines. The host has ten CPUs and 64 GiB memory. The one-worker retry reduced scheduler contention. The sequence and contents of the journey tests were preserved.
- The trace captures the end of the test body before cleanup, and writes the completed trace after cleanup. A two-click witness captured button values 1 and 2, followed by 2 at test end despite an explicit cleanup hook.
- The trace observes clients obtained from the real provider, hoisted journey fixtures, and the shared mock factory. This avoids missing requests made by journeys that build their own client. For example, the operation history, comprehensive, and empty-project suites recorded 36, 69, and 23 calls respectively during calibration.
- The plan listed nine browser-subscription calls. Source inspection found seven additional calls through its lifecycle alias. Both counts are reported. BASE was the actual branch tip at startup, which followed the plan's authoring commit.
- The unchanged production source tree has Git tree ID `f93f1c452c527b053640df246d5c2a46b8cc4787` at both BASE and the tracing commit. All ten initially untracked paths were recorded before work. `TARGET_TREE.md` remained operator-owned when rechecked.

## Verification and public API

| Check | Result |
| --- | --- |
| `make test-ui` | The clean BASE checkout, the tracing commit, and the isolated Phase 0 candidate each passed all 1,600 tests in 112 files. Final-check receipts use the `final-` prefix in `runs.tsv`. |
| `npm --prefix ui run build` | The library and demo built successfully with the lockfile dependencies. |
| Full oracle | Failed on unchanged source. Original captures and failed comparison logs are retained. |
| Graph audit | The original scanner reproduced the 9 unreachable files, 204 unused exports, 67 test-only exports, and the cycle. The extended scanner measured the additional fields in the table above. The exit targets remain open. |
| Public API | TypeScript export enumeration, the scoped frontend import check, and the demo import check passed for the existing surface. The G1 release diff is absent. |
| `make e2e` and verify-skill Builder flows | `make e2e` exited 2 at its required `dev-doctor` prerequisite. The Air container could not find `/workspace/go.mod` or `/workspace/go.sum`. The browser suite, target-bound Builder checks, and CDA checks did not execute. The earlier host-level Doctor run failed at the same prerequisite. |

The TypeScript entry exports at BASE and final code are identical. The root entry has 382 names, Viewer 24, Builder 34, and saved views 21. G1 calls for 23 removals from both the root and Builder entries. The release remains at 0.1.5. The P0.4 candidate manifest was generated and checked in its worktree. Its 23-name removal list for each affected entry is in `.cache/uiplumb/g1-removals.json`. The package branch has no committed `public-api.json`. A scoped `git grep` on IDP-Frontend's `feature/clickhouse-explorer` verified all three consumed root names, all eleven saved-view names including re-exports, and the stylesheet subpath. The demo's two dynamic entry imports also remain available.

No Phase 1–5 unit was committed. Their per-unit regression suites, oracle comparisons, production-logic adversarial reviews, and milestone browser runs did not take place. There is no semantic-identity table-test verdict. Both implementations remain at their original locations, and the divergence exception was not invoked.

## Replacement UI section for TARGET_TREE.md

> ### UI
>
> Follow `docs/UI_PLUMBING_IMPLEMENTATION_PLAN.md` for the target layout and its verification gates. Keep wire contracts and identity rules in `src/contracts/`, transport in `src/client/`, reusable resource and store primitives in `src/data/`, and formatting in `src/display/`. Keep the Viewer, saved-view builder, and Explorer Builder shells with their features.
>
> Inside the Explorer Builder, put pure transformations in `model/`, workflow state, actions, and request ownership in `session/`, external DOM subscriptions in `dom/`, and components in `ui/<domain>/`. Keep `BuilderWorkspace` as the layout shell. Consult `docs/UI_PLUMBING_EXECUTION_REPORT.md` for implementation status.

## Local commits

- `5c68879a9` — P0: Trace UI dispatches and client request lifetimes in Vitest
- This report — P0: Record the blocked UI plumbing acceptance gate

## Resumption under revised acceptance

The operator approved behavioral acceptance after reviewing the same-source trace failures.
Commit `d4799c609` updated the plan and execution instructions and added the graph and
public API guards. The protected journeys now gate acceptance alongside targeted request
and race checks. Raw traces remain available for diagnosis. Phase 0 passed all 1,600 UI
tests and built both packages in the clean worktree. The earlier traces and failure findings
above remain the historical record.
