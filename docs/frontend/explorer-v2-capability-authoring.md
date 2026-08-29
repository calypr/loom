# Explorer V2 capability and authoring handoff

This contract matches `internal/explorer/authoringv2` and the generated Loom
OpenAPI routes. The canonical wire specification is
[`openapi/openapi.yaml`](../../openapi/openapi.yaml).

## Hard cutover

The browser uses only `/authoring/v2` after a coordinated Loom/frontend
deployment. V1 authoring is not an HTTP compatibility surface. Loom may retain
the V1 decoder solely to migrate already-stored documents into V2; it must not
be probed, advertised, or used as a browser fallback.

The configurable REST base is:

```text
/api/v1/projects/{project}/explorers/{explorerId}/authoring/v2
```

The reverse-proxy prefix, host, and port remain application configuration.
Requests use the existing authenticated session and project authorization.

## Routes and exact wire shapes

| Operation | Route | Wire shape |
| --- | --- | --- |
| Capability metadata | `GET .../capability` | supported operations, preview limits, and feature flags |
| Candidate search | `POST .../suggestions` | `{snapshotToken, nodeId, query?}` → matching candidate definitions |
| Builder read | `GET .../builder` | `ExplorerBuilderState` |
| Builder commands | `POST .../commands` | atomic server-owned draft changes |
| Builder reconcile | `POST .../reconcile` | compile the exact persisted draft into an `ExplorerBuilderReceipt` |
| Direct compile | `POST .../builder` | `{workspace, snapshotToken}` → `ExplorerBuilderReceipt` |
| Preview | `POST .../preview` | `{receiptId, outputId, limit?}` → `ExplorerBuilderPreview` |
| Publish | `POST .../publish` | `{receiptId}` → `ExplorerBuilderPublication` |

POST bodies are strict: unknown fields, duplicate keys, trailing JSON, recipe
AST, selectors, AQL, physical identities, and generated columns are rejected.
Send the exact snapshot token returned by the catalog. Preview and publish take
only a server-owned receipt; they do not accept a mutable document.

## Catalog, provenance, and fail-closed enrichment

`GET builder` carries the catalog used to validate the workspace. Its wire
fields are `generation`, `authorizationScopeDigest`, optional
`resolvedSchemaDigest`, `snapshotToken`, `complete`, `nodes`, `edges`,
`candidates`, and `routePolicy`. `GET capability` is smaller metadata used for
feature discovery; it does not return the catalog.

These fields are the wire provenance. Do not invent a separate `provenance`
object or rename `generation`. The token identifies this
exact immutable generation and authorization scope. Nodes use `nodeId`, edges
use `edgeId`, and candidates use `candidateId`; all are opaque and scoped to
the token.

Required evidence is fail closed. Resource inventory, relationships, and field
enrichment must be available, complete, and untruncated before Loom exposes a
usable capability. A failed `GET capability` returns a stable
`CAPABILITY_UNAVAILABLE` diagnostic (normally HTTP 503); it does not return a
partial selectable graph. Observation counts and suggested values may enrich
a compiler-approved candidate but cannot create a selector capability.

Catalog fields are intentionally small:

- A node is `{nodeId, resourceType, rowRootEligible, rowGrain?, populated,
  documentCount?}`. `documentCount` is a non-negative count when available.
- An edge is `{edgeId, fromNodeId, toNodeId, label}` in Builder direction.
- A candidate has `candidateId`, `nodeId`, `label`, `logicalType`,
  `repeated`, `filterable`, `chartable`, `projectionModes`, and
  `defaultProjectionMode`.

Suggested values are intentionally not embedded in the catalog. When a field
control is opened, fetch them with the exact catalog `snapshotToken` and
`candidateId`. The response is `{apiVersion, kind, snapshotToken, candidateId,
values, complete, truncated}`. A stale token returns
`STALE_CAPABILITY_SNAPSHOT`; an unknown candidate returns `CANDIDATE_NOT_FOUND`.
`complete` and `truncated` describe only the bounded suggestion response and
must not be interpreted as capability completeness.

Projection values are uppercase and must be copied from the candidate. The
wire vocabulary is `VALUE`, `FIRST`, and `ALL`; migration-compatible
`SCALAR`, `ARRAY`, and `DISTINCT_ARRAY` values are also accepted. There are no
`aggregate`, `pivot`, `explode`, or lower-case wire values in this V2
implementation.

## Document and route semantics

An `ExplorerBuilderDocument` contains `kind`, `output` (`id` and
`title`), `rootNodeId`, `routeSteps`, `selections`, and `presentation`.
There is no `baseNodeId`, `rowNodeId`, `routeEdgeIds`, or authored
`routeOccurrences` in V2.

`routeSteps` is an ordered finite edge path from `rootNodeId`. Each step is
`{edgeId, occurrenceId?}`. If omitted, the server derives `step-1`, `step-2`,
and so on. The root occurrence is always `base`; occurrence identities are
derived from the path and are not a second node identity.

The route policy is explicit: `routePolicy` has `allowRepeatedEdges`,
`allowSelfLoops`, and optional `maxSteps`. When `maxSteps` is omitted, every
finite route is eligible.
There is no hidden four-hop or schema maximum. Repeated edges and self-loops
are valid when the returned policy allows them. The server still validates
edge existence and continuity.

Each selection is `{candidateId, occurrenceId?, projectionMode}`. An omitted
occurrence selects the route tail; an explicit occurrence must be one derived
by this exact route and must have the candidate's node. The same candidate can
be selected at multiple occurrences. Duplicate `(candidateId, occurrenceId)`
pairs are rejected. `presentation` is a map from non-empty client keys to
`{label?, visible?, order?}` and contains no executable fields.

## Builder state and receipt lifecycle

`GET builder` returns `{apiVersion, kind, workspace?, catalog}`. `workspace` is
nullable/omitted for an unpublished Explorer; do not require a workspace before
rendering the catalog. The catalog and workspace, when present, come from one
snapshot.

POST Builder and POST Compile validate the workspace against the exact token,
compile it, persist an immutable receipt, and return:

```json
{
  "apiVersion": "loom.calypr.org/explorer-authoring/v2",
  "kind": "ExplorerBuilderReceipt",
  "builder": {"...": "ExplorerBuilderWorkspace"},
  "receiptId": "receipt_...",
  "snapshotToken": "sha256:...",
  "generation": "generation-...",
  "intentDigest": "sha256:...",
  "compilerVersion": "loom.explorer.compiler/v7+authoring-v2-native-4",
  "outputs": [],
  "diagnostics": []
}
```

The receipt pins intent, snapshot, generation, authorization, the resolved
semantic recipe, and its public output contract. Request-scoped physical IR
and AQL are deterministically regenerated and are not persisted. Preview loads
that receipt and returns
`ExplorerBuilderPreview` with `receiptId`, `outputId`, emitted `columns`,
`rows`, and `rowCount`. Publish loads the same receipt and returns
`ExplorerBuilderPublication` with `receiptId`, `revisionId`, `state`, and
materialized `outputs`. Neither operation silently recompiles against a newer
snapshot. If the receipt's snapshot is stale or no longer authorized, preserve
the user's document, refresh Builder state, and require explicit recompile.

### Preview limits and recovery

Preview is a bounded, atomic read. Loom applies a ten-second server deadline
and rejects a response whose encoded body would exceed 32 MiB; it does not
return a partial row set. The static plan gate checks the generated plan before
execution, and representative plans are verified with Arango `EXPLAIN`. A
request that passes the gate can still fail if Arango is unavailable.

Preview failures use stable dataframe codes. `RECEIPT_STORE_UNAVAILABLE` and
`PREVIEW_TIMEOUT` are retryable; retry the same receipt after a short delay.
`PREVIEW_RESPONSE_TOO_LARGE` is not retryable with the same request: lower the
row limit or select a narrower output. `PLAN_TOO_EXPENSIVE` is also
non-retryable until the authored output is reduced. `CLIENT_CANCELED` means
an explicit server-visible context was canceled and should not be surfaced as
a server outage. Fiber cannot currently propagate every browser disconnect,
so an aborted browser request may continue on Loom for at most the ten-second
deadline. `BACKEND_UNAVAILABLE` is retryable when the dependency is
temporarily unavailable. The browser should branch on `error.code`, preserve
the latest Builder document, and choose the documented recovery action rather
than parsing the human-readable message.

## Stable diagnostics and recovery

Capability failures use `CAPABILITY_UNAVAILABLE`; stale token/receipt input
uses `STALE_CAPABILITY_SNAPSHOT` or `RECEIPT_INPUT_UNAVAILABLE`; malformed
input uses `MALFORMED_AUTHORING_REQUEST`; semantic document failures use
`INVALID_AUTHORING_INTENT`; preview uses `RECEIPT_NOT_FOUND`,
`UNKNOWN_AUTHORING_OUTPUT`, `INVALID_PREVIEW_LIMIT`, `PLAN_TOO_EXPENSIVE`,
`RECEIPT_STORE_UNAVAILABLE`, `PREVIEW_TIMEOUT`,
`PREVIEW_RESPONSE_TOO_LARGE`, `CLIENT_CANCELED`, `BACKEND_UNAVAILABLE`, or
`PREVIEW_FAILED`;
old receipt contracts use `RECEIPT_RECOMPILE_REQUIRED`; publication uses
`NO_SELECTED_COLUMNS`, `PUBLICATION_CONFLICT`, `MATERIALIZATION_FAILED`, or
`MATERIALIZATION_ACTIVATION_FAILED`.

HTTP expectations are stable: `400` malformed input, `401/403` auth failure,
`404` missing Explorer or receipt, `409` stale snapshot or publication
conflict, `413` preview response too large, `422` semantic intent or unknown
output failure,
`499` client cancellation where the deployment exposes that status, `504`
preview timeout, and `503` unavailable dependency or receipt store. Render the
safe server message and preserve the request ID; never classify by matching
prose or expose wrapped storage errors.
