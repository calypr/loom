# Construction workspace interaction design

Accepted workspace direction, 2026-09-24. Open [the interactive prototype](index.html), the [submenu specification](SUBMENUS.md), the [gap analysis](GAP_ANALYSIS.md), or the [work packages](WORK_PACKAGES.md).

The user accepted this frontend organization and asked for editable/removable operations and a gap analysis. Detailed submenu behavior remains a proposal. The prototype uses fictional records and simulated responses. Its local calculations exist only to make the design inspectable; production calculations remain backend-owned.

The audience understands the intended dataset but does not write SQL. General dataframe construction remains the product scope. Measurement records illustrate the interactions without defining the supported domains.

## Accepted interaction decisions

| Decision | User direction |
| --- | --- |
| Data loading | Use the existing Loom ingestion API. The builder constructs tables from already loaded data. |
| Starting flow | Default to available starting tables or record types. Selecting one establishes row meaning and shows its records before further construction. |
| Referenced tables | Combine retains the input version it used. Later edits do not propagate automatically; Update input previews a deliberate version change. |
| Broken dependencies | Keep the accepted result until affected steps are repaired or explicitly removed, then apply the coherent change. |
| Calculations | Guided controls and an optional formula editor manipulate the same calculation. |
| Discovery results | Show meaning, example values, and available coverage beside results, with deeper inspection. |
| Calculation default | Add a new column, with an explicit option to replace the selected column. |
| Apply and preview | Require a successful row preview before Apply, including for expensive operations. |
| Preview triggering | Run automatically after a short pause once the edit is valid. Cancel superseded requests; only the latest successful preview enables Apply. |
| New pivot categories | Show new categories and require acceptance before changing the saved columns. |
| Finish action | Save the configuration, compute the dataframe, and publish it to ClickHouse for fast querying. |
| Preview performance | Treat iterative preview latency as a core requirement with a dedicated performance work package. |

The Combine answer with a concrete example supersedes the earlier answer about live links. Preview latency targets remain proposed until measured; routine warm previews should aim for under one second.

## The accepted workspace

Use named tables for meaningful results, an editable step history for construction, and a selected operation's editor beside the result. Compare two entry layouts using the selector above the prototype:

| Layout | Benefit | Tradeoff |
| --- | --- | --- |
| A. Visible table actions | Users can discover the kinds of changes available. It takes one click to enter an operation family. | Five controls compete for space; labels need to remain understandable. |
| B. Compact task menu | The table has fewer permanent controls. | Every family takes another click to discover. A closed menu gives beginners less guidance. |

The accepted direction is A on wide screens, with the same actions grouped into a menu when space requires it. This is the user's design preference; researcher usability has not yet been observed. Both layouts also offer actions beside selected columns. The menu and selection shortcuts open the same editor. Layout B remains available for comparison.

The current table is the default view. History becomes useful after someone has constructed something. A graph of every operation does not need to occupy the initial screen. A dependency view can supplement named tables when several tables are combined.

## What occupies the workspace

| Place | Responsibility |
| --- | --- |
| Table navigation | Name the independently useful tables. An intermediate step does not automatically create a new table. |
| Table header | State what one row represents, the current scope, and whether displayed counts are exact or sampled. |
| Table actions | Show available operation families: Add columns, Keep rows, Calculate, Reshape, Combine. |
| Column selection | Offer actions appropriate to selected columns and provide entry to profiles. Selection never changes data. |
| Operation editor | Ask about one intended transformation, reveal dependent choices, and show the proposed effect. |
| Result | Keep current rows visible while a new preview loads. Switch explicitly between current and proposed results. |
| Construction history | Reopen or remove a prior operation, inspect its result, or create another table from that point. |

On a narrow screen the operation editor moves above the proposed table. The table can scroll horizontally within its own region. Table navigation and history remain reachable. A production design should retain the selected column and editor position across resizing.

## One editing cycle

1. Select a table or columns. Highlight the selection immediately.
2. Choose an offered action. Open its editor immediately using the current capability context.
3. Resolve only the choices needed for that operation. Each input shows applicable values and any unresolved state.
4. Show a proposed result as soon as sufficient meaning is specified. Later form edits supersede earlier requests.
5. Apply commits the proposal for the current inputs. Cancel discards the proposal and preserves the table.

An operation family is a navigation category. Choosing Reshape first exposes the supported transformations, such as turning values into columns. Choosing that transformation opens its editor. The full backend parameter set is never one undifferentiated form.

For a pivot, the user chooses row keys, category values that become headings, the value source, the rule for multiple contributors, and the rule for missing cells. These choices have a meaningful order. A duplicate-value rule is a deliberate decision. The interface must not quietly select the first contributor.

For a related column, the user first discovers the information, then chooses among valid constructions. If the source and output are unambiguous, the editor can show a proposed column immediately. Distinct source relationships must receive labels and examples that explain their meaning.

## The frontend needs reusable editors

The backend defines applicability, available inputs, compatible combinations, output behavior, and executable constructions. The frontend organizes those decisions into a small set of editors:

| Editor family | Reusable interactions |
| --- | --- |
| Discover and select | Search or browse source fields and concepts, inspect observed data, select a supported construction. |
| Conditions | Nested all, any, and not groups; typed comparisons; explicit scope of the condition. |
| Calculations | Select compatible inputs, construct typed expressions, name results, choose missing-value handling. |
| Summaries | Select grouping keys, contributors, and reductions; distinguish counts of records from counts of populated values. |
| Relationships | Select another input, matching meaning and keys, multiplicity handling, and unmatched-row behavior. |
| Reshaping | Configure pivot, unpivot, and expansion with explicit output identities and empty-value behavior. |
| Ordering and time | Choose ordering fields, direction, partitions or anchors, bounds, and tie behavior. |

These are a frontend presentation taxonomy, not a proposal for new backend operation types. An editor can reveal controls progressively without maintaining its own evaluator. Backend evidence drives the choices within the editor. The frontend owns wording, grouping, focus, navigation, and the presentation of that evidence.

The prototype demonstrates some of these interactions. Nested condition groups, temporal controls, arbitrary expression trees, union, publication, and a full source browser are not implemented in this design study.

## Information required at each interaction

The names below describe information, not proposed API routes. The [gap analysis](GAP_ANALYSIS.md) maps the interactions to existing contracts and identifies missing semantics.

| User event | Information required | When it should arrive | What the UI does while waiting |
| --- | --- | --- | --- |
| Open a table or stage | Revision, schema, stable column references, row meaning, capability state, operation families, first row page | Fetch capability context alongside table data; do not make it depend on completing profiles | Show the table shell. Show loading rather than an empty capability set. |
| Select one column | Its supported operation families, logical type and cardinality | Reuse information from the table context | Highlight immediately and open cached menus locally. |
| Select several columns | Valid operations for that particular selection, compatible output forms | Reuse exact cached selection context or request a bounded refinement | Retain selection. Indicate that selection-specific choices are loading; do not label them unsupported. |
| Open an operation | Typed parameters, valid source references, dependency rules, safe defaults, required decisions, relevant consequences | Open the editor with cached structure and obtain remaining context | Keep labels and completed choices visible; load only unresolved controls. |
| Browse or search available data | Authorized, paged source results, observed codes, units, support, and evidence completeness | Fetch on browse or search, retaining cached results for the same context | Keep prior results visibly tied to the prior query until replacement. Never add a column merely by browsing. |
| Select a source | Valid constructions, meaningful relationship alternatives, available value forms, contributor scope | Resolve this source against the current table and selected step | Show the selected item and load its constructions. |
| Change a parent parameter | Valid domains for dependent parameters and any preserved choices | Refine only affected choices; batch changes from one gesture | Preserve still-valid inputs. Ask for replacements in place when meaning changes. |
| Open a profile | Populated-row denominator, source frequency, distributions, units, time coverage, multiple matches, completeness | Load independently from structural capability information | Give the profile its own loading or unavailable state. The action menu stays usable. |
| Complete sufficient parameters | Proposal identity, inferred schema and row meaning, sample rows, count/effect evidence and its limits | Debounce edits and request only the current proposal | Preserve the form and show current rows. Clearly mark the pending proposal. |
| Apply | An applicable proposal matching current stage, selected inputs, and parameters | Proposal must exist before Apply is available | Prevent duplicate application. On conflict preserve the user's choices while refreshing context. |
| Edit an earlier step | Its original input, saved parameters, downstream dependencies, valid edits in that context | Fetch when that step is opened; prefetch nearby history if useful | Show the historical result and preserve current construction separately. |
| Combine named tables | Both input revisions, compatible matching operations and keys, match-cardinality evidence | Fetch only for the selected input pair | Preserve the chosen inputs. Do not infer matching semantics from identical labels alone. |

Structural capability answers and observed-data answers are different. “A numeric reduction can execute” does not require a fresh distribution scan. “This relation has exactly one match for every row” needs evidence or a declared invariant. The frontend displays the strength and scope of that evidence.

The complete downstream context also matters when editing history. An operation that is valid in isolation may remove a column required later. The backend supplies the affected steps and repair choices before Apply. Dependencies must not make the earlier step permanently uneditable. The UI must not silently delete dependent steps.

## Responsiveness requirements to evaluate later

These are proposed experience targets, not measured backend performance or promised service levels:

- Selection, opening a cached menu, and opening the editor frame should respond within about 100 ms.
- Small capability refinements should normally arrive within about 250 ms on a warm connection. A loading state must still work when they do not.
- Debounce text changes by about 200 ms. Avoid canceling and rebuilding the entire table on every keystroke.
- Aim to display a useful bounded preview within about one second for ordinary interactions. Heavy operations can take longer without blocking further editing or Cancel.
- Full-data checks and exports have separate progress. A bounded preview is never labeled a complete check.

The prototype uses 320 ms and two-second simulated preview delays. They demonstrate behavior only. There is no real backend request in this artifact.

Do not prefetch every possible combination of columns or enumerate every valid expression. Load the table's structural context, resolve the selected action and inputs, and cache by the exact context. Authorization scope, source generation, table revision, and selected step participate in cache identity. An old response cannot supply validity for a newer table.

## Loading, unknown data, and refusal

| State | User experience |
| --- | --- |
| Supported and sufficiently specified | Show the operation and enable the next meaningful interaction. |
| Supported but missing an author decision | Highlight the question that still needs an answer. |
| Capability information still loading | Retain the editor or menu position and identify the part loading. |
| Unsupported in this context | Explain the specific reason in a relevant “Unavailable here” disclosure. Avoid a screen of disabled controls. |
| Structurally executable but evidence incomplete | Keep the action available when an explicit total policy handles the uncertainty. State the evidence limit. |
| Preview request failed | Preserve selections and inputs, keep current rows, provide Retry. |
| An older response arrives | Discard it. It must not replace the current preview or enable Apply. |
| Table or authorization context changed | Refresh the context and reconcile preserved inputs. Do not present the old result as current. |

A capability proves that a construction is supported under its stated contract. It does not promise that a large execution cannot time out. Observed multiplicity, data-dependent exceptions, and incomplete checks need explicit policies and evidence. These are not reasons to let users assemble unsupported syntax and discover that only at Apply.

## History and table reuse

One committed analytical operation produces one step. Renaming a display label, opening a profile, or selecting a column does not create a construction step.

Selecting history is read-only. Edit this step reopens its parameters and shows a proposal before replacing the construction. Create table from here creates a separate editable branch. Adding a new operation while inspecting history must never silently discard later steps.

Remove this step previews the surviving construction. If later operations need its outputs, the panel identifies them and requires an explicit choice to remove them too. Other steps remain and run again. Cancel retains the construction; Undo restores it. The prototype implements one in-memory Undo for removal. Production needs persisted revisions and the downstream repair flow specified in [SUBMENUS.md](SUBMENUS.md#editable-steps).

The prototype branches by copying the construction through the selected step. The accepted Combine behavior retains the input version used by the consuming table. Later edits to that input do not change the consumer. Update input creates a proposal against the selected newer version. Within one construction, editing an earlier step still requires repair or explicit removal of broken later steps before Apply.

Reordering steps should be offered only where the backend can supply a valid move and its consequence. An unrestricted drag gesture would imply operations commute when they do not.

## What to review in the prototype

1. Switch between visible actions and the compact task menu. Inspect the same operation in both layouts.
2. Select Value, then inspect its distribution. Compare opening the local action menu with loading the profile.
3. Open Reshape, then Turn values into columns. Choose how duplicate values combine. Compare current and proposed rows before Apply.
4. Select both resulting numeric columns and calculate a ratio. Add Age at collection from Specimen details.
5. Select the first applied step, edit its reduction, and observe the later calculations update after Apply.
6. Select the first applied step and choose Remove this step. Review the dependent ratio. Explicitly include its removal, inspect the surviving rows with age, then Apply and Undo.
7. Select Slow, change a parameter, and then change it again. The form remains available, and only the latest proposal can enable Apply.
8. Select Unavailable, then retry with Quick. The editor preserves the same choices.
9. Inspect an earlier step and create another table from it. The original construction remains available.

The layout decision is accepted. The next design review concerns submenu wording, decision order, and dependency repair. The gap analysis identifies existing contracts and missing authoring semantics without prescribing indexes or package boundaries.

## Observed prototype behavior

A local Chrome walkthrough recorded 22 states in [observations.json](observations.json), with no JavaScript exceptions. This verifies the prototype, not Loom's backend.

- A pivot combined 8 source records into 4 specimen rows. The first specimen's Marker A average was 9 and its ratio to Marker B was 3.
- Editing the earlier reduction to sum changed that value to 18 and the downstream ratio to 6. The added age column remained present.
- Removing that pivot required explicitly including the dependent ratio. The age step remained, producing eight rows. Cancel preserved the original construction, and Undo restored all three steps after removal.
- An older delayed filter preview did not replace the latest condition. An unavailable preview preserved the value 55 and left Apply disabled.
- Creating a table from an earlier step preserved the original three-step construction.
- At a 390 px viewport the document width remained 390 px, and the editor appeared above the table.

Inspect the [visible actions](layout-a.png), [compact menu](layout-b.png), [pivot editor](pivot-editor.png), [removal review](remove-step-dependencies.png), [unavailable preview](preview-unavailable.png), and [narrow editor](narrow-editor.png).
