# GRAPH-ROWS-08: Define how both interfaces edit one recipe

Status: verification-in-progress (implementation and owned acceptance complete; authenticated Calypr journeys/frame remain incomplete because the required token is unavailable).
Reuse the existing persistence API from 06; its follow-up closure does not block 08.

Dependencies: the existing 01–03 implementation.

## Gap

The authoritative plan and proposal APIs exist. The tickets do not yet specify
which recipe facts each interface edits or how users combine the two interfaces.
See [the gap analysis](../GAPS.md).

## Deliverable

Define a compact coverage table for starting records, relationship occurrences,
match policy, source row shape, related-record rows, Group keys, column value
policies, filters, and other existing operations. For each fact, identify its
authoritative owner, graph presentation/action, row-expression presentation/action,
existing proposal or command, and any unsupported combination.

Both interfaces must be useful entry points. The row expression can select a
supported relationship through a picker. Graph selection can open the relevant
row or value inspector. Neither path requires users to author raw IDs or switch
to the other interface for every routine edit. Operations unique to one editor
remain visible and intelligible in the other.

Bind each editable expression term to its existing occurrence, column, or operation
and stage. Distinguish editing that fact from appending a new operation. Editing
an earlier term preserves valid downstream operations or shows the exact rejection
or repair consequence. Never rebuild the saved recipe from a shortened projection.

Related-record expansion preserves existing column identities and policies. An
existing list column stays a list, repeated for each resulting pair when applicable.
Selecting a scalar from the matched related record is an explicit column choice.
Specify the before/after values so both editors communicate the same result.

Define one active proposed change per output. Merely inspecting the other
representation preserves it. Starting another mutation requires Apply or Cancel
unless the existing API can validate the combined candidate atomically. Sequential
graph and row edits must compose into the saved recipe. A new multi-edit transaction
API is required only if a concrete first-release workflow cannot use existing APIs.

Implement the shared UI editing owner or adapter around those existing proposal
flows. Keep one output/draft/snapshot identity and one Apply-enabled candidate.
Replace indirect cross-component activation with explicit owner actions as the
editors integrate. Preserve existing cancellation and stale-response guarantees.

Define projections for both the saved recipe and the validated candidate. The
current saved-network endpoint alone cannot describe an uncommitted proposal.
Use authoritative candidate data already returned by proposals and receipts. If
that data cannot support a faithful projection, specify the smallest typed read
extension. Do not display a saved graph beside proposed row semantics as if they
describe the same state.

Own the shared UI editing contract and focused contract tests. The coordinator owns
shared API/schema changes, which require a demonstrated missing fact. Reuse server
network, occurrence, stage, and receipt identities. Derived display state is not a
second authored AST.

## Acceptance

- Map every first-release action in 09 and 10 to an existing public operation or a precisely specified contract extension.
- Show a graph-first and a row-first version of the same ResearchSubject/Observation recipe. Their canonical semantics and exact results agree.
- Preserve distinct occurrences of the same resource type and explicit construction-stage anchors in both representations.
- Separate relationship eligibility, row identity, and column multiplicity. Group rows are keyed tuples rather than fabricated resource nodes.
- Edit a saved value policy or Group key with downstream operations present. Update the intended fact rather than append a competing step; preserve valid bindings or reject without mutation.
- Specify saved, editing, preview-ready, applying, failed, and stale behavior, including switching interfaces and tables. No switch silently saves, discards, or retargets an edit.
- Existing ordinary and imported documents round-trip without inventing missing provenance or rewriting the shared recipe pin.

## Checks and handoff

Use the existing network/identity and proposal tests. Add focused checks only for
new correspondence or ownership behavior. Return the coverage table, exact shared
types, example projections, and unsupported reasons before 09 and 10 integrate.
Follow repository isolation and review rules. This ticket authorizes no deployment.

## Integrated follow-up checkpoint

Implementation and all12 final owned native cases/105actions pass on source134faf8b…,
including strict canonical graph/row equivalence and100-operation proof. Exact raw
reports, current source/review identities, original creation/admission distinction,
prior failures and limits are recorded in [VERIFICATION.md](../VERIFICATION.md).
Authenticated Calypr authoring journeys and actual267-column host frame remain
unverified because no short-lived token is available. This ticket remains
verification-in-progress; owned fixture acceptance is not substituted for host checks.
