# GRAPH-ROWS-07: Prove the connected editor with independent records and native lifecycles

Status: verification-in-progress (implementation and same-model review complete; required current-source native acceptance incomplete). Dependencies: Fixture specification can start now; final acceptance follows GRAPH-ROWS-02 through GRAPH-ROWS-06.

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

## Execution contract

Read [the work package](../README.md) and repository `AGENTS.md` before implementation. Use the agreed baseline and an isolated worktree. Preserve unrelated work. Run the smallest relevant checks, then the required final acceptance against a frozen source snapshot. Record source identity, review, commands, outcomes, and remaining gaps. Do not deploy or publish. The operator separately authorized the parent to push the completed code after functional checks pass.
