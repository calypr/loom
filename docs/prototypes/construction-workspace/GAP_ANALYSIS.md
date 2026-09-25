# Construction workspace gap analysis

Initial source-backed analysis, 2026-09-24. This records the original gap inventory and its evidence. The later [framing contract](FRAMING_CONTRACT.md) and [work packages](WORK_PACKAGES.md) govern scope; Calculate and derived-column authoring were removed from the builder plan. The accepted frontend direction is the [table workspace](index.html), with named tables, a proposed-change panel, and editable construction steps. The [submenu specification](SUBMENUS.md) defines the current questions inside each action.

The [work packages](WORK_PACKAGES.md) turn these gaps into implementation units. The finish action saves the configuration, computes the dataframe, and publishes it to ClickHouse. Preview latency is a core acceptance criterion because Apply requires a successful row preview.

## Product and design decisions

The intended user understands the domain and desired dataset but does not write SQL. The product scope is general dataframe construction. The measurement examples exercise the interface and do not limit that scope.

The current product flow defines rows, adds columns from source records, arranges those records, reviews the frame, and publishes. Selected-column shortcuts open the same editors. Every committed framing operation must reopen with its saved choices and support removal. Dataset evidence belongs in discovery, profiles, and proposed effects throughout this workflow.

First-principles redesign makes composition and editability foundational requirements. That changes the durable model needed behind the accepted layout. Model the Domain makes a saved analytical step distinct from a command request, a display setting, and a preview response.

The user clarified that Combine retains the input version used by the consuming table. Input updates are explicit. This supersedes the earlier answer about automatic propagation. An edit that breaks later steps remains a proposal until those steps are repaired or explicitly removed. Detailed submenu copy remains under review. No production execution change is included in this study.

## Source baseline and scope

The implementation baseline is `arch/integration` at `ba882b003289024c3c1c8eb0fbc0d2611fb73eeb`. Source was inspected in a clean local checkout at `/private/tmp/loom-construction-gap-ba882b003`. The working repository's planning branch is older, so reading its production files would understate existing capabilities.

Evidence references below identify paths and lines at that exact baseline. `git show ba882b003289024c3c1c8eb0fbc0d2611fb73eeb:<path>` retrieves the source. A refreshed GitNexus index provided navigation. Source inspection and focused executable checks support the findings.

This analysis distinguishes four kinds of work:

| Classification | Meaning |
| --- | --- |
| Frontend gap | Existing meaning or behavior needs a different interaction, presentation, or shared controller. |
| Contract gap | The frontend cannot request or persist the required meaning through the current authoring boundary. |
| Execution gap | The requested operation requires additional lowering or execution support. An authoring limitation alone does not establish this. |
| Unproven | Source suggests a possibility, but the reviewed path or checks do not establish it. |

## Existing work to carry forward

Loom already provides much of the backend guidance this interface needs:

| Existing capability | How the workspace uses it | Evidence |
| --- | --- | --- |
| Source construction choices with typed options and reasons | Populate **Add columns** with meaningful executable alternatives. | E03 |
| Table-shape capabilities, role-specific choices, operators, operands, policies, and availability | Populate shape editors before a proposal. Arithmetic capability remains existing code outside builder scope. | E04 |
| Saved table-shape intent and derived references | Reopen a saved shape with its authored names, choices, and inputs. | E04, E06 |
| Category discovery, resolution, proposal, and application | Preserve explicit category selection and preview before mutation. | E04, E05, E06 |
| Add, replace, and remove modes for a saved table shape | Reuse existing edit/remove behavior while extending it to general steps. | E06 |
| Row-definition choices and proposals | Explain and edit what one row represents using server choices. | E05 |
| Source inspection, semantic inventory, population mapping, and cell trace | Put data understanding beside source selection and results. | E05, E07 |
| Column transformations, contributor policies, and time windows | Reuse supported controls inside the appropriate operation editor. | E07 |
| Typed recipe predicates, including comparisons and boolean operations | Supply existing execution support for scoped record filters. | E11 |
| Grouped and expanded row definitions with compiler output | Reuse grouping and expansion while making them composable operations. | E12 |
| Context checks, preserved edits after failure, and stale-proposal rejection | Use these controller rules across operation families. | E06 |

The gap is substantial, but it is not “build a capability API” or “make shapes editable.” Those already exist. Their applicability to arbitrary intermediate results needs to expand with the construction model.

## The structural gap behind editable steps

The current durable `Document` stores a root, routes, row definition, population, columns, and one `TableShape`. That shape stores one optional pivot or unpivot followed by derived constructions. Workspace documents are explicitly independent table intents. [E01]

This representation does not directly express an arbitrary sequence such as filter → pivot → group → expand → join another constructed table. Existing command identifiers support mutation retries; they are explicitly not durable analytical step identities. [E02]

Adding a history list in React would therefore be insufficient. The production design needs these durable meanings:

| Concept | Required meaning |
| --- | --- |
| Table | A named construction with a selected result and revision. |
| Step | A stable identity, an operation kind, saved parameters, and explicit inputs. Editing preserves the step identity. |
| Input | A source, an earlier step output, or a referenced table result with a defined revision policy. |
| Output | Stable column references, types, row meaning, and provenance. Display names can change independently. |
| Proposal | A candidate change tied to the input revisions and parameters, with inferred outputs and affected dependents. |
| Accepted revision | The coherent saved construction that Preview, reopening, evidence, and export refer to. Undo restores a prior revision. |

These are required semantics, not a chosen Go package structure or an endpoint prescription. Existing compilation and execution remain the authority. The next architecture decision must establish which steps lower into existing operations and which require execution changes. Preview and final output must use the same execution path.

The user sees an ordered history within a table. Dependencies between tables form a graph underneath. A graph canvas is optional inspection, not required authoring navigation.

## Frontend and backend gap register

The identifiers are stable so implementation work can refer back to a specific interaction. “New” describes the accepted workspace requirement at this authoring boundary. It does not assert that no reusable primitive exists deeper in the engine.

| ID | Interaction | Frontend work | Backend or contract work | Current evidence |
| --- | --- | --- | --- | --- |
| CW01 | Navigate tables and act on the current result | Build the accepted shell, table-first arrangement, selection, and shared right-side editor. Move existing settings into those editors. | Reuse table identity and existing authoring operations. No new query engine is implied by layout. | Partial. Current Builder places preview after configuration panels. E08 |
| CW02 | Save meaningful editable steps | Render summaries, selected historical results, and saved parameter editors. | Persist operation identity, inputs, ordering, and output references. Rehydrate them without reconstructing meaning from a command log. | New semantic-step contract. E01, E02 |
| CW03 | Keep transforming a derived result | Offer the same actions on each supported intermediate stage. | Address and compile intermediate outputs. Extend physical plan composition, stage scopes, and renderer layout while reusing operator lowering. | Both authoring and compiler composition constraints are established. Physical validation allows only one reshape and one root scan in an ordinary plan. E01, E13 |
| CW04 | Edit, remove, and undo earlier steps | Show impact, preserve choices, offer repair or explicit dependent removal, retain unrelated steps, and restore prior revisions. | Compute dependencies and proposed affected results. Apply the coherent edit atomically and persist undoable revisions. | Existing shape replace/remove is reusable. General history, repair, and Undo need a contract. E02, E06 |
| CW05 | Use another constructed table | Add named input/version selection, branch-copy labels, and explicit Update input review. | Retain immutable input versions and their source identity. Reject invalid dependency cycles and preserve referenced versions. | Independent documents and duplicate-table already exist. Versioned constructed-table inputs remain a gap. E01, E02 |
| CW06 | Explore valid actions before submitting | Group server choices around row, column, and related-record decisions. Refine dependent inputs without discarding valid selections. | Supply capabilities for a selected stage and selection. Extend current typed choice/resolution contracts where needed. | Partial and reusable. A single new generic endpoint is not yet justified. E03–E06 |
| CW07 | Discover fields, concepts, and related summaries | Unite browse, search, observed codes, relationship choices, and supported output forms inside **Add columns**. | Reuse semantic inventory and source construction choices. Generalize their input context when CW03 changes it. | Existing support. Stage generality and aggregate evidence scope need verification. E03, E05, E07 |
| CW08 | Keep rows with composable conditions | Build typed conditions, nested groups, related-record quantifiers, duplicate handling, and ranked-row selection. | Persist predicates on arbitrary stage outputs. Expose valid operators and missing semantics. | Resource-field filters already lower to typed and physical predicates. General stage filtering and its editable condition tree remain contract gaps. E01, E07, E09, E11 |
| CW09 | Withdrawn: derived-column authoring | No builder work. Calculate, formula editing, recoding, and imputation are outside the framing contract. | Existing arithmetic code may remain, but is not a builder deliverable. | This row is retained only so earlier references to CW09 do not silently change meaning. |
| CW10 | Change row and column meaning | Rehouse pivot/unpivot controls, category discovery, output names, and policies. Specify group and repeated-value editors. | Reuse pivot/unpivot, repeated-value expansion, and explicit-group support. Add general grouping over stage columns and ordered composition where needed. | Existing GroupRows reads a pinned explicit-group revision. It does not by itself provide arbitrary GROUP BY over current-stage columns. Repeated shape composition also needs compiler changes. E01, E04, E06, E12, E13 |
| CW11 | Join, append, and compare tables | Build input-pair matching, unmatched-row and multiplicity choices, column alignment, and row-growth review. | General table-input joins, append, and membership operations need authoring contracts and verified lowering. Existing resource relationships are narrower. | General constructed-table combination is not represented in current documents. Engine reuse is unproven here. E01, E02, E03 |
| CW12 | Understand the dataset while constructing it | Add consistent profiles, denominators, completeness labels, changed-row evidence, and links to contributors. | Reuse source evidence, population mapping, and cell trace. Establish profiles and before/after evidence for arbitrary stages. | Partial. Existing evidence must be reconciled by scope and revision. E05, E07 |
| CW13 | Preview and recover without losing edits | Apply one pending/current/proposed interaction across all editors. Preserve forms, selection, and last accepted result on errors. | Extend receipt/proposal identity to general stages and dependent edits. Keep proposal identity coupled to exact parameters. | Existing shape controller provides much of the pattern. Uniform operation coverage is missing. E05, E06 |
| CW14 | Receive near-instant valid choices | Cache exact contexts, prefetch bounded structural facts, cancel superseded work, and keep slow evidence independent. | Measure current paths. Separate bounded capability work from scans where necessary. Add indexes only for identified slow access paths. | Latency unmeasured. Client cache infrastructure exists, but relevant authoring calls use direct requests. E04, E05, E10 |
| CW15 | Save, publish to ClickHouse, reopen, and inspect | Reopen saved steps, expose publication progress, and identify the accepted configuration in result/evidence views. | Carry composition identity through persistence, compilation receipts, ClickHouse materialization, evidence, and query/export. | Existing publication lifecycle should be reused. Agreement for the proposed general step model is unproven until implemented. E01, E05, E06 |

Column rename, reorder, display visibility, and removal controls already exist. They need consistent placement and identity behavior in the new workspace. **Hide in this view**, **Remove from dataset**, and **Remove this step** must remain distinct actions. [E02, E07]

## Information calls derived from the menus

All paths below are relative to `/api/v1/projects/{project}/explorers/{id}/authoring/v2`. These are existing client methods at the source baseline. The table describes reuse, not a requirement to preserve every HTTP call boundary. [E05]

| User event | Existing path or information | Remaining requirement |
| --- | --- | --- |
| Browse source information | POST `/semantic-inventory`; POST `/column-source` | Present one discovery flow. Establish the applicable intermediate-stage context. |
| Select a source | POST `/construction-choices` | Retain exact source, relationship, snapshot, and output identity. Put reasons beside alternatives. |
| Change what a row represents | GET `/row-definition-choices`; POST `/row-definition-proposals` | Integrate the row-meaning interaction with shape operations and history. |
| Open Reshape | POST `/table-shape-capabilities` | Reuse applicable shape choices and saved intent. Extend to the selected general stage when that exists. |
| Choose pivot source columns | POST `/table-shape-category-discoveries` | Preserve pair-specific discovery, explicit selections, evidence scope, and output mappings. |
| Complete a supported shape | POST `/table-shape-resolutions`; POST `/table-shape-proposals` | Preserve staged resolution and proposal identity beneath one editor. |
| Preview accepted output | POST `/preview` | Reuse bounded output previews. Define intermediate-stage addressing and changed-result comparisons. |
| Inspect result provenance | POST `/population-mapping`; POST `/cell-trace` | Connect evidence to the displayed stage and distinguish source counts from output counts. |
| Select columns for any family | Some facts exist in output schemas and specialized capability responses | Define the minimal shared selection context and family availability. Avoid independent frontend type rules. |
| Edit or remove an arbitrary earlier step | Shape replacement/removal covers a restricted case | Add stage identity, saved parameters, dependency impact, repair choices, and revision restoration. |
| Combine constructed tables | No general table-input authoring equivalent established | Add capabilities for the selected input pair, explicit revisions, and supported match/append meanings. |

Opening a menu must not wait for a population-wide profile. Prefetch structural context alongside the table, then request bounded refinements for selected inputs. A changing parent field invalidates dependent choices, not the entire panel.

Cache identity includes authorization context, data snapshot, workspace revision, selected stage, relevant inputs, and parameters. Authorization changes clear reusable results. A late response cannot enable Apply for a newer proposal.

The earlier design's 100 ms local interaction, 250 ms warm capability refinement, and roughly one-second useful preview are proposed experience targets. No backend latency measurement in this study establishes those values. A supported construction may still encounter an execution timeout or changed source state. Such failures preserve the edit rather than turning unsupported syntax into a normal discovery mechanism.

## Dependency behavior is part of the contract

The [removal prototype](remove-step-dependencies.png) demonstrates a specific rule. Removing a pivot also identifies a ratio that consumes the pivot outputs. The user can explicitly remove that ratio. An unrelated age-addition step remains and runs on the surviving rows. Undo restores the previous construction.

Production needs the same reasoning from the backend's semantic dependencies. A step may depend on row meaning, ordering, contributor scope, or another table revision as well as column IDs. Scanning names or waiting for compilation errors is insufficient.

Changing an earlier step must not permanently lock it because later steps exist. Preserve the accepted result while the user edits a proposal. If a dependency breaks, show the affected operation and offer repair or explicit removal before committing the revised construction. The submenu specification details this interaction.

Remove is a mutation of the construction. It is not a new analytical step that computes an inverse of the removed operation. Undo restores a recorded revision. The prototype stores this information only in memory and models dependency detection with fixture validation.

## Decisions before work packages

The user settled these product choices. Additional accepted interaction details are recorded in [DESIGN.md](DESIGN.md#accepted-interaction-decisions):

| Decision | Accepted behavior |
| --- | --- |
| A constructed table consumes another table | Retain the input version used. Later edits to the other table have no effect until an explicit input update. |
| An earlier edit breaks later operations | Preserve the accepted result while the user repairs or explicitly removes affected steps, then apply the coherent change. |

The concrete Combine example supersedes the earlier answer about live links. Ordinary table constructions remain independent. A branch made by copying a construction remains a copy. Editing earlier steps within one construction still requires review and repair of its later steps.

The following engineering questions belong to implementation planning, not the user:

| Question | Required planning output |
| --- | --- |
| How can an operation consume a previously constructed result? | Identify the exact compiler/recipe boundary to extend and demonstrate lowering through existing operators. Establish whether composition needs new execution machinery. |
| How are steps and revisions persisted? | Define stable step/output references, saved parameters, atomic mutation, rehydration, and restoration of a prior accepted revision. |
| Which submenu operations lack primitives? | Map each operation to its existing authoring and execution support. Name actual gaps separately from missing UI exposure. |
| How do valid choices and evidence address an intermediate result? | Specify the required stage context and map existing capability, discovery, preview, and trace calls to it. |
| Which information calls miss the responsiveness target? | Measure representative structural-choice paths separately from data scans before proposing cache or index changes. |

Work packages can then separate construction persistence/lowering, the workspace shell, shared editor/capability behavior, operation-family editors, table composition, and evidence/reopen/export integration. Each package needs named contracts, dependencies, and an observable completion check. The compiler boundary and actual operator inventory determine effort; the number of menu items does not.

## Work that can follow from this analysis

The next units are concrete and have different purposes:

1. **Review the submenu contract.** Resolve labels, decision order, missing/multiplicity policies, and downstream repair using `SUBMENUS.md`. These decisions determine what information each panel requires.
2. **Specify persistent steps and inputs.** Settle CW02–CW05 with a saved-construction schema, revision rules, dependency semantics, and a lowering map into the existing execution path. Do not implement a second frontend evaluator.
3. **Build the accepted shell around existing capabilities.** CW01, CW06, CW07, CW10, and CW13 can reuse today's controls and services. Do not present a temporary frontend event log as persisted history.
4. **Close framing gaps against the submenu contract.** CW08, CW10, and CW11 identify construction operations requiring representation and lowering decisions. An operator inventory establishes reuse before new engine work is scheduled. CW09 remains withdrawn.
5. **Measure the interaction paths.** Instrument capability, discovery, proposal, profile, and preview separately. CW14 turns observed slow paths into focused caching, query, or index work.

This ordering is about dependencies between deliverables. It does not narrow the product to one dataset or make the prototype's arithmetic implementation a production starting point.

## Verification

The standalone prototype was driven in local headless Chrome. [observations.json](observations.json) records 22 states with no JavaScript exceptions. The walkthrough exercised pivot, calculation, a related column, editing an earlier step, dependent and independent removal, Cancel, Undo, late responses, unavailable preview, branch copying, and a narrow viewport. The dependency-removal screenshot was visually inspected.

Focused tests ran against the clean implementation baseline. The UI files `TableShapeEditor.unit.test.tsx`, `tableShapeController.unit.test.ts`, and `ColumnSelector.unit.test.tsx` passed all 46 tests. These cover existing saved shape controls, input references, and column editing. They do not verify the proposed general step model.

Focused backend checks passed for authoring shape round trips and removal, lifecycle capability discovery and saved-shape reload, chained arithmetic, proposal replacement and idempotency, recipe/expression packages, shape compilation, expression lowering, and the HTTP capability contract. No real institution dataset, backend latency benchmark, general composed execution, or researcher usability study was run for this design analysis.

These checks establish reusable behavior. They do not establish implementation duration. The conversational estimate of 2–4 agent-weeks was not supported by a sized work breakdown and is withdrawn. Effort estimates require resolving intermediate-result compilation and identifying actual missing operators first.

## Source evidence

All locations refer to the baseline SHA above.

- **E01. Durable shape.** `internal/explorer/authoringv2/types.go:23` defines `Document`; line 70 describes independent table intents. `internal/explorer/authoringv2/table_shape.go:17` defines one reshape and derived constructions. Lines 22–44 restrict the reshape payload to pivot or unpivot. Lines 93–107 define binary derived operands.
- **E02. Authoring commands.** `internal/explorer/authoringv2/commands.go:17` lists commands. Lines 56–59 state that the command ID is a retry token and never durable authoring identity.
- **E03. Source choices.** `internal/explorer/capability/construction.go` defines source construction choices and their supported options. `internal/explorer/lifecycle/construction_choice.go` resolves application against the authorized source and current snapshot.
- **E04. Shape capabilities.** `internal/explorer/lifecycle/table_shape_capabilities.go:66` defines column type/unit facts. Lines 82–103 define choices, availability, and saved intent. `GetTableShapeCatalog` at line 118 compiles the base context and stores a bound catalog receipt.
- **E05. Client authoring routes.** `ui/packages/loom-ui/src/api.ts:957` defines the authoring prefix. Lines 1065–1151 expose inventory, source inspection, construction choices, row definitions, shape discovery/resolution/proposal, preview, population mapping, and cell trace.
- **E06. Existing shape editor lifecycle.** `ui/packages/loom-ui/src/features/ExplorerBuilder/components/TableShapeSettingsPanel.tsx:30` defines controller states. Lines 255–306 resolve add/replace/remove proposals and guard application by context and available comparison. `tableShapeController.ts:65` builds the scope key. `TableShapeEditor.tsx` and `GroupedPivotEditor.tsx` implement saved controls and explicit category/policy choices.
- **E07. Existing column and source controls.** `ui/packages/loom-ui/src/features/ExplorerBuilder/components/ColumnSelector.tsx` exposes column rename/reorder/remove and source inspection. `FeaturePolicyEditor.tsx:399` labels aggregate choices. Its contributor and window editors begin at lines 660 and 777. These scoped controls are not a general stage-expression contract.
- **E08. Current workspace arrangement.** `ui/packages/loom-ui/src/features/ExplorerBuilder/BuilderWorkspace.tsx:1431` starts row-definition settings. Source selection appears at line 1490, columns at line 1625, shape settings at line 1820, and preview at line 1843.
- **E09. Filter scopes.** `internal/explorer/authoringv2/types.go:31` separates rows, population, columns, and fixed filters. `ui/packages/loom-ui/src/features/ExplorerBuilder/components/PresentationPanels.tsx` presents shared and fixed filter settings. `FeaturePolicyEditor.tsx` edits contributor predicates. Their scopes must not be conflated with arbitrary Keep rows steps.
- **E10. Client request caching.** `ui/packages/loom-ui/src/api.ts:886` implements `getCached` with request deduplication and cancellation handling. The authoring methods at lines 1065–1151 call `request` directly. This establishes existing cache infrastructure and a place to investigate, not measured poor performance.
- **E11. Existing predicates and expressions.** `internal/dataframe/semantic/recipe_filter.go:12` lowers resource-relative recipe filters to typed predicates. `internal/dataframe/compiler/lower/root_projection.go:448` lowers the associated physical predicate. `internal/dataframe/expression/expression_checking.go:302` checks if/case, boolean, and comparison calls. `internal/dataframe/recipe/json_encoding.go:48` distinguishes lexical source-document context from a reference to another constructed table; line 54 defines nested recipe expressions.
- **E12. Existing grouping and expansion.** `internal/explorer/authoringv2/row_definition.go:11` defines records, groups, and expanded row definitions. `internal/explorer/compilation/semantic_compile.go:487` emits recipe output with `GroupRows` and `Expand`. General repeated placement of these operations is not represented by those fixed output fields.
- **E13. Compiler composition constraints.** `internal/dataframe/recipe/document_types.go:37` and `internal/dataframe/semantic/recipe_plan_types.go:31` represent resource-rooted outputs with fixed shaping fields. `internal/dataframe/compiler/ir/physical_validation.go:213` rejects multiple table reshapes; lines 264–275 constrain root sources and terminals. `internal/dataframe/compiler/render/aql/navigation_layout.go:100` handles shapes within a fixed navigation layout. `internal/dataframe/compiler/ir/physical_operations.go:351` provides correlated subplans, which are not a generic reference to an independently constructed output. `internal/dataframe/recipe/document_types.go:71` identifies GroupRows as an immutable explicit-group source.
