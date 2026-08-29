# Explorer authoring

The public Builder contract is
`loom.calypr.org/explorer-authoring/v2`. The browser submits canonical V2
workspace documents with typed column sources, route occurrences, and
presentation intent. Loom owns recipe construction, authorization predicates,
physical planning, output contracts, and AQL.

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
| Read Builder | `GET /builder` | capability plus saved/active V2 workspace when present |
| Save draft | `PUT /draft` | `{workspace, snapshotToken, expectedDraftVersion, expectedDraftDigest?}` |
| Export workspace | `GET /export` | canonical portable V2 workspace |
| Compile | `POST /compile` | `{workspace, snapshotToken}` |
| Compile alias | `POST /builder` | same operation for the current frontend |
| Suggestions | `GET /capabilities/:snapshotToken/candidates/:candidateId/suggestions` | bounded values for one exact snapshot |
| Preview | `POST /preview` | `{receiptId, outputId, limit?}` |
| Publish | `POST /publish` | `{receiptId}` |

There is no public V1 authoring route or lazy legacy migration path.

## Builder workspace

A V2 workspace contains ordered output documents and tabs. Each document has a concrete row-root node, an ordered finite
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

Loom stores canonical V2 workspace drafts with draft-version and optional
digest compare-and-swap. Repository/ETL publication advances that same version,
so an external replacement produces `DRAFT_CONFLICT` instead of silently
overwriting browser work. Compile-before-preview is the safe fallback when the
latest editor state has no receipt. Receipts remain immutable and
content-addressed.

Repository publication accepts the same V2 workspace and uses the same receipt
compiler as browser publication. Publish materializes and verifies the exact receipt before activating its
revision. A materialization, output-verification, or activation failure leaves
the previous active revision intact. Active revisions retain the canonical V2
document so Builder reads can restore it directly.

## One-time V2 artifact migration

Revisions published by an earlier V2 server may predate the revision-level
copies of the workspace, receipt ID, public output contract, or emissions. Run
the explicit migration once; normal Builder and Viewer reads never mutate
stored revisions.

Dry-run one revision first:

```bash
go run ./cmd/explorer-v2-artifact-migrate \
  -revision authoring_a46ec8372e6675684b3f58dfa0bf73f9956e3310259a149de5642c76f639677a
```

Apply after the report says `REPAIRABLE`:

```bash
go run ./cmd/explorer-v2-artifact-migrate \
  -revision authoring_a46ec8372e6675684b3f58dfa0bf73f9956e3310259a149de5642c76f639677a \
  -apply
```

`ARANGO_URL`, `ARANGO_DATABASE`, `ARANGO_USERNAME`, and `ARANGO_PASSWORD` are
accepted from the environment. Omitting `-revision`, `-project`, and
`-explorer` scans all affected revisions. The migration derives
`receipt_<hash>` only for `authoring_<hash>` revisions, requires matching
project, Explorer, recipe, generation, and existing artifacts, fills only
missing fields, and reports conflicts without modifying them. Re-running it is
idempotent.

## Authorization and generation rules

Compilation requires the snapshot's generation to be active. Preview may use a
retained inactive generation if the caller remains authorized for the exact
scope captured by the snapshot. Publication additionally requires the receipt
generation to remain active.

Restricted-empty authorization is not equivalent to unrestricted access. Loom
propagates the explicit scope mode and paths through deterministic lowering and
execution. The frontend never supplies those predicates.

## Editable traversal trees and ordinary columns

The V2 route is a finite occurrence tree. Builder clients may add sibling and
nested children through exact catalog edges and may attach ordinary primitive
field columns to any occurrence. `Document.Route` is the only durable route
source; compatibility-only linear route helpers are not an authoring contract.

Compile and preview may persist and execute immutable receipts, but neither
operation changes the active Explorer revision. Publication is the only
Builder operation that materializes outputs and switches the active revision.

## Cleanup

Receipts referenced by immutable revisions are retained. Loom exposes receipt
count and approximate-byte statistics and retains explicit orphan purge. There
is no automatic TTL until deployed storage measurements justify one.
