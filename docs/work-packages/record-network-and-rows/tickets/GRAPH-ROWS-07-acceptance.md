# GRAPH-ROWS-07: Prove the connected editor with independent records and native lifecycles

Status: verification-in-progress (implementation and owned acceptance complete; authenticated Calypr journeys/frame remain incomplete because the required token is unavailable).

Dependencies: Fixture specification can start now; final acceptance follows GRAPH-ROWS-02 through GRAPH-ROWS-06.

## Problem

Component checks and successful page loads do not prove record multiplicity, persistence, or the interaction between graph edits and row definitions. The presentation baseline also has explicit native acceptance gaps.

## Change

Define a small independent fixture and expected outputs before the implementation chooses queries. Use the bundled Helm recipe for real import and editor checks. Register repeatable native workflows through the current verification entry points.

Include a subject with no related matches, one with a single match, and one with several matches. Add two distinct paths to the same resource type and two independent branches with two and three matches. Specify expected IDs, values, missing behavior, and contributor identities by hand.

Complete the baseline source-step edit/reload/restore and Group preview/cancel checks. Then cover the integrated record-network and row-definition workflow. Run authenticated Calypr acceptance after the owned fixture cases pass.

Own fixtures, oracle tables, verifier registration, and native tests. Keep mutable QA data, runtime ownership, logs, and source snapshots isolated from implementation workers.

## Acceptance

- Prove row-preserving related values, required-match row loss, deliberate expansion, grouped key tuples, retained contributor values, and unsupported branch combinations.
- Assert exact returned values and identities independently of the compiler. A two-by-three match fixture detects accidental Cartesian products.
- Native controls prove Preview, Cancel without mutation, Apply, earlier-step edit, removal, reload, and stale-draft recovery. Required actions cannot be replaced by injected events.
- Check readable labels, keyboard paths, no clipped controls, and a useful preview at 1536×900. Resize taller and back. Stress a 100-operation plan and deliberate catalog navigation.
- Treat sampled data and exact totals separately. Use the operator-authorized 30s functional action/request bounds and report actual timing separately; five seconds is not a hard acceptance requirement.
- Capture first-failure diagnostics, final source fingerprint, API identity, exact commands, exits, and raw evidence. Do not store credentials in logs or commits.
- Final integrated acceptance runs against the frozen implementation snapshot. Reuse unaffected evidence, but do not use the pre-label presentation screenshot as final acceptance.
- If publication behavior changes, use the existing publication and Viewer correctness checks. Publication itself requires the operator's authorization.

## Follow-up for the finished two-interface workflow

Preserve the existing imported and ordinary scoped passes. The new contract in
[08](GRAPH-ROWS-08-shared-editing-contract.md) and UI work in 09–11 need their own
current-source proof. Extend the existing native cases and independent fixture
oracles. Keep one acceptance runner.

1. Graph-first: author a supported path, then define one row per subject and related values as a list. Assert exact subject rows and values in the shared preview.
2. Row-first: choose the equivalent connections and row/value policies in Define a row. Open the graph and assert the actual occurrences, edge identities, match modes, and stage annotations. Compare canonical semantics and results with graph-first.
3. Mixed: switch between both editors, deliberately create subject–observation rows, inspect repeated subject values and empty-match behavior, Apply, reload, and edit through the other interface. Include Group key tuples in a separate compatible stage.
4. Ownership: inspect the other representation during a proposal, attempt a conflicting edit, Cancel, change tables, and reproduce one real stale draft. Assert the exact saved plan and candidate ownership throughout.
5. Composition: in the authenticated Calypr navigation frame at 1536×900 and 1536×1100, prove the selected traversal, editable row expression, active inspector, proposal controls, and useful result preview stay legible. Include an open proposal and bounded 100-operation history.

Native assertions must inspect visible expression terms and their changes. An
accessible button name alone cannot prove the row editor exists. Assertions must
also read the saved plan and exact returned identities and values independently.

Specify column behavior across expansion with literal records. For example,
subject S1 has observations O1=7 and O2=9. Its existing observation-list column
contains `[7, 9]`. Expanding into `(S1, O1)` and `(S1, O2)` keeps that column as
`[7, 9]` on both pairs. A separately chosen per-match scalar column contains 7
and 9. Check column identities and policies as well as values. Adapt the example
to the independent fixture's actual records without deriving expected values from
the compiler. Include an edit to an earlier expression term and prove it updates
the intended fact without duplicating operations or losing downstream work.

Known missing fixture values remain distinct from loading, errors, and unavailable
projections. A screenshot containing dashes does not identify which state occurred.

Run the authenticated doctor and drive before the complete Calypr lifecycle. Record
the frontend checkout, `LOOM_UI_SOURCE` resolution or equivalent bundle identity,
active Loom image, recipe pin, target, and tested source. A standalone fixture pass
or Next page HTTP 200 does not establish Calypr acceptance. Keep missing test
credentials explicit without treating the operator's browser login as failed auth.

Finish retained carry-in, related-row, upstream-dependency, and history cases on
the relevant final snapshot. Reuse unaffected evidence with its exact scope. The
existing 30s functional bounds, zero retries, and source/receipt integrity checks
remain in force. Publication and deployment need their own authorization.

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
