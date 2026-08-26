# Explorer authoring

The public Builder contract is
`loom.calypr.org/explorer-authoring/v2`. The browser submits semantic intent
using opaque node, relationship, candidate, occurrence, and receipt IDs. Loom
owns recipe construction, output-column identities, authorization predicates,
physical planning, and AQL.

For the complete wire contract and frontend checklist, see
[Explorer V2 capability authoring](frontend/explorer-v2-capability-authoring.md).
For the compiler and receipt internals, see
[Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md).

## Endpoints

All routes are under:

```text
/api/v1/projects/:project/explorers/:explorerId/authoring/v2
```

| Operation | Route | Contract |
| --- | --- | --- |
| Read capability | `GET /capability` | exact active compiler-proven snapshot |
| Read Builder | `GET /builder` | capability plus active V2 document when present |
| Compile | `POST /compile` | `{document, snapshotToken}` |
| Compile alias | `POST /builder` | same operation for the current frontend |
| Suggestions | `GET /capabilities/:snapshotToken/candidates/:candidateId/suggestions` | bounded values for one exact snapshot |
| Preview | `POST /preview` | `{receiptId, outputId, limit?}` |
| Publish | `POST /publish` | `{receiptId}` |

There is no public V1 authoring route. V1 types and the V1 compiler exist only
to migrate already stored legacy authoring documents.

## Builder document

A V2 document contains one output, a concrete row-root node, an ordered finite
route, occurrence-specific field selections, projection modes, and presentation
preferences. It contains no FHIR selector path, recipe expression, traversal
alias, collection name, AQL, or generated output column.

Route steps select only relationship IDs advertised by the capability snapshot.
Each repeated route position has its own occurrence ID, so selecting a field on
one repeated resource is not ambiguous with another. Empty routes, finite long
routes, repeated edges, and self-loops work only when the snapshot advertises
them.

## Compile and receipts

Compile validates the exact active snapshot and authorization scope, translates
the document directly to a resolved Loom recipe, persists an immutable receipt,
and returns the receipt ID plus compiler-owned emitted columns. The same
canonical input is idempotent.

An editor state with no selected columns may compile so the frontend can
reconcile intermediate edits. It cannot publish; Loom returns
`NO_SELECTED_COLUMNS`.

Preview and publish require the receipt. They do not accept mutable authoring
intent and do not silently compile against a newer catalog or generation.
Artifactless receipts created by older servers return
`RECEIPT_RECOMPILE_REQUIRED`.

## Draft and publication behavior

The browser may keep its mutable V2 document locally and debounce compile.
Compile-before-preview is the safe fallback when the latest editor state has no
receipt. Concurrent compile responses cannot overwrite each other because each
receipt is immutable and content-addressed.

Publish materializes and verifies the exact receipt before activating its
revision. A materialization, output-verification, or activation failure leaves
the previous active revision intact. Active revisions retain the canonical V2
document so Builder reads can restore it directly.

## Authorization and generation rules

Compilation requires the snapshot's generation to be active. Preview may use a
retained inactive generation if the caller remains authorized for the exact
scope captured by the snapshot. Publication additionally requires the receipt
generation to remain active.

Restricted-empty authorization is not equivalent to unrestricted access. Loom
propagates the explicit scope mode and paths through deterministic lowering and
execution. The frontend never supplies those predicates.

## Cleanup

Receipts referenced by immutable revisions are retained. Loom exposes receipt
count and approximate-byte statistics and retains explicit orphan purge. There
is no automatic TTL until deployed storage measurements justify one.
