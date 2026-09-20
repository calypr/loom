# S03 row proposal contract

Status: implementation input after the expansion and grouping compilers can execute candidate workspaces.

## User-visible transaction

The Builder treats a row-definition change as one transaction.

1. The user selects **Records**, **Groups**, or **Expand repeated values**.
2. The browser submits opaque backend-issued choice IDs and explicit policies.
3. Loom validates the request against the saved draft, the capability snapshot, and any immutable group revision.
4. Loom compiles and executes both the saved workspace and the proposed workspace without changing the draft.
5. Loom returns a bounded comparison with row examples, member witnesses, affected columns, and count completeness.
6. **Cancel** discards the response and sends no mutation.
7. **Apply** sends the proposal ID through the existing command endpoint with the expected draft version and digest.
8. Loom revalidates the proposal and commits only the row definition through the existing draft compare-and-swap.

The browser never sends a FHIR path, cardinality claim, traversal, resource-specific rule, or resolved row definition.

## Request model

`ProposeRowDefinitionRequest` identifies one saved output and one exact draft. It contains the project and Explorer route values, `snapshotToken`, `expectedDraftVersion`, `expectedDraftDigest`, `outputId`, a bounded preview limit, and one `RowDefinitionSelection`.

`RowDefinitionSelection` is a closed union.

- `RECORDS` has no payload.
- `FIELD_GROUP` has a row choice ID and a missing-key policy.
- `EXPLICIT_GROUP` has an immutable group revision ID and an unassigned-member policy.
- `EXPANDED` has a row choice ID and an empty-collection policy.

The lifecycle service converts the selection to `authoringv2.RowDefinition`. The conversion resolves row choice IDs through `capability.ResolveRowChoiceID`. The explicit-group arm loads and validates one complete immutable group revision. No transport type duplicates the FHIR schema.

## Proposal proof

The preview compiler persists an immutable candidate compilation receipt. The response returns the candidate receipt ID as an opaque proposal ID.

The candidate receipt must prove all of these facts:

- It belongs to the requested project and Explorer.
- It uses the requested capability snapshot, source generation, schema digest, and authorization scope.
- Its authoring bundle differs from the saved workspace only at the selected output's row definition.
- Its intent digest equals the digest of that complete candidate workspace.
- Any explicit group revision in the bundle remains complete and matches the receipt's pinned revision, definition, membership, selection, and scope digests.

Apply accepts only `outputId` and `proposalId` inside an `APPLY_ROW_DEFINITION_PROPOSAL` command. A lifecycle preparer loads the receipt and extracts the resolved row definition. The reducer receives that value through an unexported command field. A lifecycle checker recomputes the candidate workspace digest before `SaveDraft`.

This design follows the existing interpretation-candidate boundary. It does not add a mutable proposal store or trust a client copy of the resolved definition.

## Preview response

`RowDefinitionProposal` returns these values:

- `proposalId`, `outputId`, `snapshotToken`, `draftVersion`, and `draftDigest`.
- The selected mode and backend-provided presentation text.
- Before and after row counts with `EXACT` or `SAMPLED` completeness on each count.
- Bounded row examples with stable row IDs and exact member or item witnesses.
- Affected columns with `PRESERVED`, `NEEDS_REPAIR`, or `INCOMPATIBLE` status and a backend-provided reason.
- Notices for excluded missing keys, preserved empty parents, empty groups, overlapping membership, and unassigned members.

A record-row witness contains one root resource reference. A grouped-row witness contains the group ID plus the ordered source member references. An expanded-row witness contains the parent identity, the reached owner identity, the exact route edge identities, the repeated scope identity, and the item ordinal. The API represents these as Loom row evidence, not FHIR types.

The response labels a count as `EXACT` only when execution completed the count over the authorized population. A bounded preview never infers a whole-population count from its sample.

## Mutation rules

`APPLY_ROW_DEFINITION_PROPOSAL` must be the only command in its batch. The command uses the normal command ID replay rule and the normal draft compare-and-swap.

Apply fails without mutation when any of these facts changed:

- The draft version or digest.
- The capability snapshot, schema digest, source generation, or authorization scope.
- The candidate receipt identity or content.
- The selected output or any non-row portion of its document.
- An explicit group revision or any digest pinned by that revision.

Cancel has no endpoint and no storage action. Closing the panel has the same behavior as Cancel.

## Ownership

The existing packages keep their current responsibilities.

- `internal/explorer/capability` issues and resolves schema-backed row choices.
- `internal/explorer/authoringv2` owns the durable row-definition union and the pure reducer command.
- `internal/explorer/compilation` compiles the complete candidate workspace and records row-definition proof in the receipt.
- `internal/explorer/lifecycle` owns proposal validation, dual preview execution, proposal comparison, receipt revalidation, and compare-and-swap preparation.
- `internal/server` translates the OpenAPI request and response. It contains no FHIR logic.
- The UI renders generic row modes, choices, evidence, and policies. It submits opaque IDs.

Do not extend the current root-rebase `RowChangeProposal`. Root rebase changes the route tree and population route. A row-definition proposal changes row grain while keeping that authored route intact.

## Required tests

The implementation must prove these behaviors:

- Proposal generation does not call `SaveDraft`.
- Cancel sends no command and leaves the draft unchanged.
- Apply changes only `Document.Rows` and increments the draft once.
- A concurrent write makes the proposal stale and prevents every mutation.
- A tampered proposal receipt, output ID, row choice, group revision, or capability snapshot fails closed.
- Equal repeated values at different ordinals produce different expanded row IDs.
- Overlapping groups retain the same source member in each selected group.
- Empty explicit groups remain visible in the preview.
- Independent repeated collections do not multiply when only one scope is selected.
- The UI parses generic response types without FHIR resource branches.
- J03 proves propose, cancel, apply, reload, export, and literal source-member reconstruction for two schema shapes.
