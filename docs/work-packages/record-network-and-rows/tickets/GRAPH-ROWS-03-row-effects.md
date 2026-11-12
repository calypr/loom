# GRAPH-ROWS-03: Compute explicit row identity and the effects of relationship edits

Status: missing-contract. Dependencies: GRAPH-ROWS-01.

## Problem

A graph connection does not explain whether output rows stay the same, disappear, or multiply. Root resource names and sampled counts do not fully describe grouped rows or repeated-item rows.

## Change

Expose a server-owned description of row identity at each relevant stage and a proposal impact contract. Reuse `RowDefinition`, row-change assessments, construction capabilities, and receipt-bound previews. Keep identity separate from the readable sentence.

Describe the exact relationship between the proposed network, the chosen row definition, and each column's value policy. Distinguish row-preserving related values from explicit `RELATED_EXPAND`, grouping, and repeated-field expansion. Separate match eligibility from multiplicity.

Distinguish source `Rows.EXPANDED` for an item within one occurrence from construction `RELATED_EXPAND` for related records. Construction `GROUP` supplies field-key grouping; do not advertise the currently rejected source `Rows.GROUPS/FIELD` variant.

For imported documents, change the authoritative source or an explicit construction operation and regenerate derived row state. Never patch projected Rows independently of the source pin.

Return supported choices and concrete unsupported reasons. Label exact counts, sampled observations, and unknown multiplicity separately. Do not infer universal cardinality from a sample or resource-type name.

Own row-definition and impact logic in `internal/explorer/authoringv2`, `internal/explorer/lifecycle/row_change.go`, relevant capability services, and focused tests. Integrate shared API schema changes through the parent.

## Acceptance

- Subjects connected to several observations can retain one row per subject. List, count, presence, and supported summary choices preserve that identity.
- Construction `RELATED_EXPAND` produces the declared related-record rows for each input row and repeats upstream values. Assert complete row identities when two input rows share a related record. Do not label those results globally unique related records. Source `Rows.EXPANDED` has separate item-scope and empty-collection tests.
- Construction `GROUP` describes the complete key tuple and missing-key policy. Named source groups use `Rows.GROUPS/EXPLICIT` and preserve empty-group behavior. `Rows.GROUPS/FIELD` remains unavailable with a precise reason.
- Two independent branches with two and three matches never silently produce six rows. Reject unsupported expansion combinations with a precise reason.
- A change that filters unmatched records reports row loss even when the row identity shape stays the same.
- An ordinary row-root preservation or replacement updates row identity and occurrence bindings consistently. An imported preserve-mode root change stays blocked unless a typed source-root overlay exists. Explicit replacement discloses that it detaches the pin and resets to the existing replacement document. Imported compilation must agree with the effective source.
- All effects bind to the same output, draft, snapshot, proposal, and receipt as the displayed preview.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy, publish, or push as part of this ticket.
