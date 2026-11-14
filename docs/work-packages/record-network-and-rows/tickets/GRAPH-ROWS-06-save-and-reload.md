# GRAPH-ROWS-06: Preserve the complete plan through save, reload, and concurrent edits

Status: verification-in-progress (implementation and owned acceptance complete; authenticated Calypr journeys/frame remain incomplete because the required token is unavailable).

Dependencies: GRAPH-ROWS-02; GRAPH-ROWS-03; integrate GRAPH-ROWS-04 and GRAPH-ROWS-05.

## Problem

The main graph, row definition, and operation history must reload as one consistent plan. Existing imported pins, legacy routes, and construction stages cannot drift into competing sources of truth.

## Change

Complete persisted migration and transactional ownership for the contract in 01. Preserve stable output, occurrence, operation, and field-binding identities where the semantics remain the same. Preserve immutable source pins and their digests. Persist authorized changes in the project-local draft/source-plan overlay with current draft conflict checks.

Round-trip imported and legacy documents through the production save and reload paths. Restore graph selection and row meaning from saved state without replaying user edits. Keep purely visual collapse and selection state separate from authored semantics.

Handle upstream removal, row-root changes, table switches, delayed responses, failures, and concurrent editors through the existing proposal ownership and cancellation rules. Reconcile recovery must not resurrect a removed relationship or apply an old proposal.

Own migration, lifecycle ownership, and persistence tests. UI ownership corrections go through the shared workspace integration owner.

## Acceptance

- Add and edit a relationship, change row definition, add Group and column operations, save, and reload. The complete authored plan and exact results survive.
- Imported source edits preserve the original recipe reference and digest and persist an authorized project-local draft/source-plan overlay. They do not create a registry revision or modify the shared default.
- Restore values and semantics after a reversible source edit. Record legitimate revision changes rather than claiming the draft identity never changed.
- Two editors applying against the same saved draft produce a conflict or valid recovery, never a lost update.
- Cancel, failed compile, rejected dependency repair, and late responses leave the saved plan intact.
- Source-root replacement explicitly discloses detachment from the imported pin. Preserve-mode imported root edits remain blocked until the declared source-root overlay exists.
- Older accepted documents load under an explicit tested migration. Unsupported versions produce a precise diagnosis.
- Route removals and row changes preserve unrelated workspace outputs and shared bindings or require explicit repair.

## Follow-up for complementary editors

The core persistence implementation and its scoped evidence remain reusable.
Close the following behavior after [08–11](../GAPS.md) integrate. Add a production
repair only when these checks demonstrate a defect.

- Add a relationship in the graph, define values and row grain in Define a row, then edit the relationship in the graph again. Save and reopen both representations with the same occurrences, value policies, operation order, row identities, and exact results.
- Start the equivalent recipe in Define a row and continue in the graph. Compare canonical semantics and results rather than requiring unrelated creation runs to generate identical IDs.
- Merely switching presentation preserves a valid pending proposal. Starting a conflicting edit follows 08's explicit Apply/Cancel rule. Table switches, failures, stale responses, and conflicts cannot apply an old candidate to a new owner.
- Operations outside the new editor's editable subset remain represented and unchanged. A partial projection must never serialize over the complete saved plan.
- Derived graph and row-expression state reload from the authoritative draft. Persisting a second graph or expression AST is not a valid fix.

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy or publish. The operator separately authorized the parent to push the completed code after functional checks pass.

## Integrated follow-up checkpoint

Implementation and all12 final owned native cases/105actions pass on source134faf8b…,
including strict canonical graph/row equivalence and100-operation proof. Exact raw
reports, current source/review identities, original creation/admission distinction,
prior failures and limits are recorded in [VERIFICATION.md](../VERIFICATION.md).
Authenticated Calypr authoring journeys and actual267-column host frame remain
unverified because no short-lived token is available. This ticket remains
verification-in-progress; owned fixture acceptance is not substituted for host checks.
