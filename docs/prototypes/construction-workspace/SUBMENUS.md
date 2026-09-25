# Construction workspace submenu specification

Interaction contract under refinement, 2026-09-24. The user accepted the workspace layout, explicit updates of versioned table inputs, repair before Apply, guided calculations with an optional formula editor, and discovery results with meaning, examples, and available coverage. The later [ML dataset contract](ML_DATASET_CONTRACT.md) supersedes generic operation families as the primary navigation; the menus below remain advanced or contextual editor specifications. Other details below remain proposals. The [gap analysis](GAP_ANALYSIS.md) distinguishes existing support from required work.

## Shared editor behavior

Each toolbar action opens a short menu of intentions. Each intention has a name and one sentence describing its effect. Selecting an intention opens the **Proposed change** panel beside the table. Column shortcuts open that same panel with the selection filled in.

The panel contains these sections, in order:

1. **Intention.** A title and a sentence describing what changes.
2. **Inputs.** The relevant columns, records, or other table. The current selection supplies initial values.
3. **Meaning.** The questions that define the operation. Later questions appear when their inputs are known.
4. **Exceptions.** Required choices about missing values, multiple matches, or ties appear beside the choice that creates them. Optional settings use a disclosure.
5. **Proposed effect.** New columns, changed row meaning, affected later steps, and evidence about the result.
6. **Cancel** and **Apply change**. Apply belongs to the current valid proposal. Cancel preserves the current construction.

The entire editor remains reachable while preview loads. The user requires a successful row preview before Apply. Changing a parameter preserves every other choice whose meaning remains valid. A conflicting choice stays visible with an explanation and a replacement selector. The editor never silently substitutes a new meaning.

Once an edit is valid and sufficiently specified, preview runs automatically after a short pause. A newer edit cancels superseded requests. Only the latest successful preview can enable Apply. The current table and entered values remain visible while the preview runs.

The backend supplies typed choices and their applicability. The frontend owns these editor layouts and wording. Backend choice responses do not need to describe arbitrary UI widgets.

Supported intentions appear first. **Unavailable here** lists relevant alternatives with specific reasons, such as “Requires two numeric columns.” Loading and unknown states have their own presentation. Lack of a loaded response does not mean an operation is unsupported.

## Add columns

**Panel introduction:** “Bring more information into each row.”

| Intention | Description |
| --- | --- |
| Find information | Browse fields, observed codes, and concepts available for these records. |
| Summarize related records | Add a count, total, or other summary of matching records to each row. |
| Reuse a saved calculation | Choose a saved definition and connect its inputs to this table. |

**Find information** opens a search and browse view inside the panel. Results show the meaning, source, value type, units when relevant, and available coverage evidence. A result can be inspected before it is selected. Search results distinguish a code observed in the source from a concept that is known but not observed there.

After selection, the editor asks these questions:

1. **Which information?** Retain the selected field or concept and its source.
2. **How does it relate to these rows?** Present named relationship choices when more than one interpretation is valid.
3. **Which matching records contribute?** Offer an optional condition and, where supported, a time window.
4. **What should each row receive?** Offer supported forms such as a value, a count, a flag, a list, a sum, or an ordered first value.
5. **What happens with several matches or no matches?** Require a policy whenever the selected form needs one.
6. **What is the column called?** Suggest a name derived from the selected meaning. Keep it editable.

An unambiguous direct field can go from selection to proposal without six separate screens. Ambiguity expands the relevant question in the same panel.

**Information needed before choices:** authorized source inventory, applicable relationship alternatives, supported output forms, contributor fields, logical types, units, ordering fields, and policy choices. Coverage loads separately. Each statistic identifies its scope and denominator.

**Saved step example:** “Added latest measurement before collection.” Reopening restores the source, relationship, window, ordering, missing policy, and output name. A source label alone cannot reconstruct the step.

Adding columns preserves the existing row identity. A choice that produces more rows belongs to **Reshape** or **Combine**, with its row effect stated before Apply.

## Keep rows

**Panel introduction:** “Choose which records belong in this table.”

| Intention | Description |
| --- | --- |
| Match conditions | Keep rows that satisfy rules about their values. |
| Match related records | Keep rows based on the presence or contents of related records. |
| Remove duplicates | Keep one row for each chosen combination of values. |
| Keep ranked rows | Keep a chosen number of rows, overall or within each group. |

**Match conditions** starts with one readable condition, such as `[Age] [is at least] [18]`. The user can **Add condition** or **Add group**. A group explicitly means **All**, **Any**, or **None** of its conditions. Nested groups remain editable and collapsible.

The field determines the available comparisons and value editor. Numeric fields receive numeric comparisons. Dates receive dates or supported relative expressions. Categories offer search over observed values, with an explicit way to enter an allowed value that was not observed. Missing is a distinct condition, not an empty text value.

The editor states what happens when a comparison encounters a missing value. Negating a condition does not silently change the missing-value policy.

**Match related records** first asks whether there must be **At least one**, **No**, **Every**, or a supported number of matching records. Conditions inside one related-record group apply to the same related record. Separate groups may be satisfied by different records. The editor describes what “Every” means when there are no related records.

**Remove duplicates** asks which columns define equality and which row survives. If retained non-key values can differ, a deterministic rule is required. **Keep ranked rows** asks for a count, ordering, optional groups, and tie behavior.

**Information needed before choices:** comparable fields and types, supported predicate composition, allowed relationship scopes and quantifiers, category values, ordering capabilities, and supported missing/tie policies. Counts of retained and excluded rows are proposal evidence with an exact or sampled label.

**Saved step example:** “Kept rows with age at least 18 and any qualifying measurement.” The full condition tree is saved. A Viewer display filter does not substitute for a construction step that changes the dataset.

## Calculate

**Panel introduction:** “Create or update values using the columns in this table.”

| Intention | Description |
| --- | --- |
| Calculate a value | Combine numbers, dates, or text into a new value. |
| Set values by condition | Assign values when rules match, with an explicit fallback. |
| Recode values | Map existing values or categories to new ones. |
| Handle missing values | Fill missing values using a chosen value or supported calculation. |
| Calculate across rows | Use ordered or grouped rows to calculate ranks, changes, or running values. |

**Calculate a value** starts with selected inputs and compatible operations. Each argument accepts a column, a typed literal, or a nested calculation. More complex expressions expand in place. Parentheses and nested inputs have visible boundaries. The user accepted an optional formula editor that shares the same expression structure with the guided controls. SQL is not required for composition.

**Set values by condition** displays ordered rule rows with a result for each rule and a required **Otherwise** value. **Recode values** displays observed inputs, a search or selection control, replacement values, and a policy for values outside the mapping. Units and code-system identity accompany values when relevant.

**Handle missing values** distinguishes a fixed replacement, another column, and a supported group-based estimate. The panel states which rows supply an estimate. **Calculate across rows** asks for the value, grouping, order, frame or offset, and tie behavior. Ordering the displayed grid alone does not define an analytical window.

Every calculation defaults to **Add a column**, with an explicit **Replace values in a column** option when supported. Replacement is recorded as a reversible transformation. It does not overwrite source records.

**Information needed before choices:** function signatures, input and output types, supported nesting, units, allowable casts, partition/order capabilities, references to earlier derived outputs, and exception policies. Compatible next inputs come from the backend's current expression context.

**Saved step example:** “Calculated marker ratio.” Reopening restores the expression and policies, including missing operands and division by zero. Renaming either input does not break its identity.

## Reshape

**Panel introduction:** “Change the table shape. Choose what the new rows and columns should represent.”

| Intention | Description |
| --- | --- |
| Summarize into groups | Make one row for each group and calculate summaries. |
| Turn values into columns | Make selected category values into named columns. |
| Turn columns into rows | Stack selected columns into a name column and a value column. |
| Expand repeated values | Make a separate row for each item in a repeated value. |

**Summarize into groups** asks what defines one output row, then offers repeatable summary rows. Each summary specifies its contributors, calculation, name, and missing policy. “Number of records,” “Number of populated values,” and “Number of distinct values” are separate choices. A summary can be used by another operation after Apply.

**Turn values into columns** asks these questions:

1. **What identifies a row?** Choose grouping keys.
2. **Which values become headings?** Choose a category field, inspect discovered categories, and select the categories to include.
3. **What fills each cell?** Choose a value field and any relevant unit.
4. **When several records fill one cell?** Choose a supported reduction or explicit refusal policy.
5. **When no record fills a cell?** Choose a supported missing-cell policy.
6. **What about categories outside the selection?** State their treatment explicitly.
7. **What are the output names?** Review the selected category-to-column mapping.

Discovered categories are paged and scoped to the selected category/value pair. A sample of popular values never silently becomes “all values.” The accepted proposal freezes the resulting column identities or records a separately supported dynamic-schema policy. Refreshing discovery does not silently change saved headings.

**Turn columns into rows** asks which columns to stack, which keys to retain, how the source column names map to row labels, and what to call the label/value columns. Mixed input types require an offered common output type or explicit conversion. Empty input cells have an explicit retain/drop policy.

**Expand repeated values** asks which repeated field to expand, what happens to empty lists, and whether to retain item position. Expanding several fields requires a choice between pairing items and producing combinations when both are supported. Row multiplication is shown before Apply.

**Information needed before choices:** current stage schema, valid keys, source pairs, output forms, reduction and missing policies, category discovery, units, inferred output identities, and row effects. The same editor works on a source table or a previously derived table when execution supports that input.

**Saved step example:** “Made columns from measurement.” Reopening restores selected categories, output names, and every policy, even when later source discovery returns different frequencies.

## Combine

**Panel introduction:** “Use another table to build this one.”

| Intention | Description |
| --- | --- |
| Add matching columns | Match rows in another table and bring over selected columns. |
| Append rows | Stack tables with an explicit alignment of their columns. |
| Compare membership | Keep rows that also appear, or do not appear, in another table. |
| Make combinations | Create rows from supported pairs of records in two tables. |

Every intention starts by selecting the other named table or an available source. Derived tables are eligible inputs when the backend can execute the composition. Table names alone do not imply a relationship.

**Add matching columns** asks these questions:

1. **How should records match?** Choose a supported relationship or explicit pairs of matching fields. Additional supported range/time conditions appear here.
2. **Which rows remain?** Choose the supported treatment of unmatched rows on each side, described in plain language.
3. **What happens with several matches?** Choose to expand rows, summarize matches, select an ordered match, or require a unique match where supported.
4. **Which columns should be added?** Select outputs and resolve name collisions.

Candidate keys can be suggested from declared relationships. Observed uniqueness is labeled with its scope. Identical column names and a small preview do not establish a reliable match.

**Append rows** displays a mapping from each input's fields to output columns, including the type and missing behavior. It asks whether to retain duplicates and whether to add a source-table column. **Compare membership** asks for matching fields and whether matches or nonmatches remain. **Make combinations** explicitly describes its pairing rule and possible row growth.

**Information needed before choices:** compatible input revisions, match modes, key types, declared relationships, supported cardinality policies, column alignments, and cycle detection. Match coverage and row growth estimates load as separate evidence.

**Saved step example:** “Added age from specimen details.” Reopening restores the input table reference, match meaning, outputs, and policies. The consuming table retains the input version it used. Update input explicitly proposes a different version. The prototype's simple specimen join demonstrates the panel; it does not establish support for general joins.

## Table and column menus

The left navigation contains named useful tables and **New table**. Data is already loaded through the existing API. The accepted starting flow offers available starting tables or record types; selecting one establishes row meaning and shows its records. A new construction can also copy an existing one. **Create table from here** copies the construction through a selected historical step. A Combine operation records an explicit reference to another table's result.

The table menu offers Rename, Duplicate, inspect inputs, and Delete. Combine retains the selected input version. Update input previews a deliberate change to a newer version. Removing a table from navigation must not silently delete an immutable input version used by another construction. Version retention is a backend lifecycle requirement.

Column menus offer a profile and relevant transformation shortcuts. Rename and visual position are lightweight metadata edits. **Hide in this view** changes display only. **Remove from dataset** changes the construction and can affect later operations. These actions have separate labels.

## Editable steps

Each committed analytical operation receives a stable step identity and a readable summary. The selected step exposes **Edit**, **Remove**, and **Create table from here**. A compact menu can hold less frequent actions. Selection and profile inspection never create steps.

Editing uses the original inputs and saved meaning of that step. The panel states which later steps will be recalculated. Preserving their input references is the ordinary case.

When an edit removes an input needed later, the panel lists the affected steps and their missing inputs. **Review affected steps** opens those saved editors within the proposed change. Users can repair references or explicitly remove affected operations. The current committed construction remains available while this proposal is incomplete. A later dependency must not make an earlier operation permanently uneditable.

Removal has these outcomes:

| Dependency state | Behavior |
| --- | --- |
| No later operation needs the removed outputs | Preview the surviving construction, then remove the selected step. |
| Later operations need removed outputs | List them. Offer repair or an explicit choice to remove those operations too. Never cascade silently. |
| Another table references this result | Preserve the version that table uses. Editing this construction does not update the consuming table. |

Unrelated later steps remain and are recalculated against the surviving input. The proposal shows any changed row meaning. **Cancel** keeps the original construction. **Undo** restores the prior accepted revision rather than replaying guessed inverse transformations.

The prototype implements removal with an explicit dependent-step choice, retained unrelated steps, Cancel, and one session-local Undo. Repairing downstream inputs, persisted revisions, live table references, and general move-step support are specified here but remain outside the prototype.

Step reordering is available only through a backend-supported move with an impact preview. Arbitrary drag-to-reorder would conceal changes in meaning.

## Dataset understanding

Evidence appears where it informs a choice. The table header identifies row meaning, source scope, and preview completeness. Column profiles expose missingness, distributions, codes, units, and the applicable denominator. Source discovery shows what is observed before a column is added. Proposal comparisons show changes caused by an operation.

Selecting a displayed result can lead to its contributing records and saved construction. A count of source records, a count of distinct entities, and a count of populated output rows retain different labels. Unavailable evidence stays unavailable; it never displays as zero.

These evidence views share the table revision and source context with the construction. They load independently so a distribution scan does not prevent opening a valid operation menu.
