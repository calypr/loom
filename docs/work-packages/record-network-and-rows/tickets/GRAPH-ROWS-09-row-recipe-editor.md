# GRAPH-ROWS-09: Edit the recipe as an explicit row definition

Status: verification-in-progress (implementation integrated; current-source native and authenticated acceptance incomplete). Dependencies: GRAPH-ROWS-08; reuse 03 and 05.

## Gap

The current row-settings panel launches operations. It does not present a coherent
editable expression of the selected table's recipe. A row sentence plus a settings
link does not meet the two-interface goal.

## Deliverable

Build **Define a row** as a structured expression with editable terms. Start with:

```text
One row per: ResearchSubject
Connected records: Patient → Observation
Include: Observation value as a list
When there is no observation: keep the subject row
```

The example is display notation, not a new wire format. Use actual field labels,
occurrence choices, and supported policies from the current catalog. Let users
select connections here without drawing them first. Distinguish selecting a path
from defining column values and the output row unit.

Support explicit related-record rows and Group key tuples through the same
representation. Show the input stage and empty or missing policy. Keep source row
shape distinct from the row identity after each transformation. Existing operation
inspectors can supply the detailed controls. Changes use 08's proposal ownership.

Own new row-expression components and focused interaction tests. Reuse row,
Group, and column editors where they fit. The workspace owner integrates the
entry point. Compare a compact sentence form with a clause form before selecting
the layout; both prototypes use the same actual recipe.

## Acceptance

- Starting from a saved default output, a user can read what a row represents, change an editable term, and preview the consequence without visiting advanced setup.
- Select a supported relationship and include a many-valued field as a list. Rows retain their identity. Then deliberately choose related-record rows and expose repeated upstream values and the empty-match policy.
- Group displays every key, summaries, and retained values separately. Group keys remain visible after the field catalog closes.
- Edit an existing expression term through its original occurrence, column, or operation identity. Earlier edits preserve valid downstream work or expose the exact rejection or repair; they never append a duplicate operation implicitly.
- For related-record expansion, show existing list columns remaining lists on each output pair. Adding a per-match scalar is a separate explicit column selection with its own identity and policy.
- Reopen a graph-authored recipe here and recover its exact paths, match modes, value policies, and ordered operations. Uneditable existing operations remain represented and survive an edit to another term.
- Show proposed semantics separately from the saved recipe. Cancel preserves the saved plan; Apply uses the previewed candidate and current draft identity.
- Control names include the edited fact and current value. Keyboard users can edit terms and return to the shared result preview.

## Checks and handoff

Use the existing independent row fixtures and row/Group tests, plus focused checks
for expression projection and edits. Ticket 07 owns the full row-first and mixed
native paths. Deliver the real editor, not a static paraphrase or raw recipe JSON.
Follow repository isolation and review rules. This ticket authorizes no deployment.

## Integrated follow-up checkpoint

The shared owner, editable row expression, authored traversal and composed workspace
are integrated. Current automated checks and scoped same-model reviews are recorded
in [VERIFICATION.md](../VERIFICATION.md). The 12-case native matrix remains pending
after layout and saved-preview repairs, with ordering repaired and remaining current native cases pending; authenticated Calypr acceptance is blocked by a
missing short-lived token. Historical 01–07 evidence does not close these follow-up
criteria. This ticket remains verification-in-progress.
