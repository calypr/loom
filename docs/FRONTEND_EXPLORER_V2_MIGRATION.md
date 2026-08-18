# Frontend Explorer V2 Migration

This guide describes the frontend contract for Loom's REST Explorer lifecycle.
The Builder should use these REST routes for Explorer management and authoring.
It should not depend on the legacy GraphQL Explorer mutations or on generated
GraphQL candidate-input types.

## The important model change

Loom now has two Explorer ownership modes:

| Explorer | Owner | Config returned by Loom | Frontend behavior |
| --- | --- | --- | --- |
| `default` | Repository / ETL initially; Builder may edit and publish | `baselineConfig` contains a presentation-free projection; `draftConfig` and `activeConfig` contain the editable/published packet | Render and edit `draftConfig`; use `activeConfig` for the published view and fall back to `baselineConfig`/dataset metadata when needed. |
| Custom ID | User / Builder | `draftConfig` and, after publication, `activeConfig` contain the complete V2 packet | Render and edit the lossless presentation config. |

Repository packets may be baseline-only or may include a complete presentation
layer. When ETL includes `views`, filters, charts, shared filters, or file
actions, Loom preserves them losslessly and exposes the published packet as
`default.activeConfig`. `default.baselineConfig` remains presentation-free.

The default Explorer response is still useful for readiness and publication
state. Its `dataset.outputs[].columns` and `emittedColumns` describe the live
materialization. The frontend should derive the default table from those
columns and apply local presentation hints such as labels and ordering.

Custom packets may contain all V2 presentation fields, but Loom remains
authoritative for schema, readiness, publication state, and materialization
metadata. Treat a custom config as invalid or stale when its referenced output
or column is not present in the live dataset.

## API base URL

The routes are rooted at:

```text
/api/v1/projects/:project/explorers
```

In the deployed Calypr frontend this is normally reached through the Loom
reverse-proxy prefix, for example:

```text
/loom/api/v1/projects/HTAN_INT-BForePC/explorers
```

Keep the prefix configurable. Do not hard-code the training host or a port
forward into frontend code.

All requests use the application's existing authenticated session. Preserve
cookies, bearer headers, and the project authorization context used by the
rest of the frontend.

## Explorer state

The frontend should model the response as server state, not as a GraphQL model:

```ts
type ExplorerManagement = "REPOSITORY" | "INTERACTIVE";

interface ExplorerState {
  project: string;
  explorerId: string;
  management: ExplorerManagement;

  // Present for the repository default. It is always the presentation-free
  // recipe/schema baseline.
  baselineConfig?: ExplorerConfigV2;

  // The published packet is renderable. Edit draftConfig and publish it to
  // replace activeConfig.
  activeConfig?: ExplorerConfigV2;

  // Present for custom Explorers and for the repository default.
  draftConfig?: ExplorerConfigV2;

  draftVersion: number;
  draftDigest: string;
  activeRevisionId?: string;
  recipeDigest?: string;
  resolvedSchemaDigest?: string;
  sourceGeneration?: string;
  emittedColumns?: EmittedColumn[];
  materializations?: Materialization[];
  dataset?: DatasetMetadata;
  publication?: PublicationMetadata;
  diagnostics?: Diagnostic[];
  publicationState?: string;
  activeUrl: string;
  updatedBy?: string;
  updatedAt: string;
}

interface DatasetMetadata {
  generation?: string;
  schemaDigest?: string;
  outputs: DatasetOutput[];
}

interface DatasetOutput {
  name: string;
  state: string;
  queryable: boolean;
  columns?: PhysicalColumn[];
}

interface PublicationMetadata {
  state: string;
  generation?: string;
  executionId?: string;
  revisionId?: string;
  updatedAt?: string;
}

interface Diagnostic {
  severity: string;
  code: string;
  fieldPath?: string;
  message: string;
  retryable?: boolean;
  requestId?: string;
}
```

`activeUrl` is Loom's resource URL for the Explorer lifecycle record. It is
not necessarily the browser route used to render the Builder page.

## REST lifecycle

### Read the Explorer list

```http
GET /api/v1/projects/:project/explorers
```

Returns an array of `ExplorerState` values. The default is ordered first,
followed by custom IDs. Use this route for the Explorer picker and then fetch
the selected detail record.

### Read one Explorer

```http
GET /api/v1/projects/:project/explorers/:explorerId
```

The `default` Explorer is editable. Render `draftConfig` in the Builder and
use `activeConfig` for the currently published view. If either packet is
absent or has no views, use `baselineConfig` and derive the layout from
`dataset`.

### Create a custom Explorer

```http
POST /api/v1/projects/:project/explorers
Content-Type: application/json

{
  "name": "Cohort Explorer",
  "title": "Cohort Explorer",
  "source": "default"
}
```

The server derives the stable ID (`cohort-explorer`) from the name. The ID is
durable and becomes part of the URL. Do not generate a random browser ID or
send a client-selected ID as the identity source.

To create an empty custom packet instead:

```json
{
  "name": "Scratch Explorer",
  "blank": true
}
```

Creation saves a draft only. It does not compile, materialize, or publish.
The response is the new `ExplorerState`.

### Save a draft

```http
PUT /api/v1/projects/:project/explorers/:explorerId/draft
Content-Type: application/json

{
  "config": { "...complete ExplorerConfig V2...": "..." },
  "expectedDraftVersion": 3,
  "expectedDraftDigest": "sha256:..."
}
```

This endpoint validates and canonicalizes the packet, computes its digest, and
saves only the draft. It does not compile, materialize, or change the active
revision. `expectedDraftVersion` is required; send `expectedDraftDigest` when
available.

The same endpoint applies to `default`. Keep `explorer.id` as `default` and
keep `explorer.management` as `repository`; the repository identity describes
where the config originated, not whether Builder edits are allowed.

On conflict, Loom returns HTTP 409:

```json
{
  "error": {
    "code": "DRAFT_CONFLICT",
    "message": "Explorer draft changed; refresh before saving",
    "currentVersion": 4,
    "currentDigest": "sha256:...",
    "updatedAt": "2026-08-17T19:00:00Z"
  }
}
```

The frontend must refresh and reconcile the user's edits. Do not silently
overwrite, retry with the new version, or attempt a client-side merge unless
the user explicitly confirms it.

### Compile authoring selections

```http
POST /api/v1/projects/:project/explorers/:explorerId/authoring/compile
Content-Type: application/json

{
  "output": "DocumentReference",
  "config": { "...complete ExplorerConfig V2...": "..." },
  "snapshotToken": "loom:generation-token",
  "selectedCandidateIdsByNode": {
    "root": ["opaque-candidate-id"]
  },
  "expectedDraftVersion": 3
}
```

The response contains:

```json
{
  "project": "HTAN_INT-BForePC",
  "explorerId": "cohort-explorer",
  "config": { "...canonical V2 packet...": "..." },
  "draftDigest": "sha256:...",
  "snapshotToken": "loom:generation-token",
  "recipeDigest": "sha256:...",
  "resolvedSchemaDigest": "sha256:...",
  "sourceGeneration": "generation-id",
  "emittedColumns": [],
  "diagnostics": []
}
```

Compile is a validation/authoring operation. It does not save the draft or
publish data. The server resolves opaque candidate IDs through Loom's catalog;
the browser must not send AST nodes, physical table names, or browser-generated
selector IDs.

The snapshot token and expected draft version protect against compiling against
a stale dataset or stale draft. Refresh the Explorer and discovery state when
Loom returns a conflict or an incomplete/unresolved catalog diagnostic.

### Preview without publication

```http
POST /api/v1/projects/:project/explorers/:explorerId/preview
Content-Type: application/json

{
  "config": { "...complete ExplorerConfig V2...": "..." },
  "output": "DocumentReference",
  "limit": 25,
  "draftDigest": "sha256:..."
}
```

A successful response is shaped like:

```json
{
  "project": "HTAN_INT-BForePC",
  "explorerId": "cohort-explorer",
  "output": "DocumentReference",
  "columns": ["id", "subject_id"],
  "rows": [{"id": "...", "subject_id": "..."}],
  "rowCount": 1,
  "digest": "sha256:...",
  "diagnostics": []
}
```

`limit` defaults to 25 and must be between 1 and 1000. Preview is scoped to
the requested output and never changes draft, revision, materialization, or
active-pointer state.

Preview failures are terminal responses, not an indefinite loading state. They
return HTTP 422 with empty `columns`/`rows` and a diagnostic such as
`PREVIEW_FAILED`:

```json
{
  "columns": [],
  "rows": [],
  "rowCount": 0,
  "digest": "sha256:...",
  "diagnostics": [{
    "severity": "ERROR",
    "code": "PREVIEW_FAILED",
    "message": "..."
  }]
}
```

### Publish an Explorer

```http
POST /api/v1/projects/:project/explorers/:explorerId/publish
Content-Type: application/json

{
  "expectedDraftVersion": 3,
  "expectedDraftDigest": "sha256:..."
}
```

Publish reads the server-owned draft. The frontend does not send a config in
this request. Loom recompiles the draft, materializes every referenced output,
checks that every output is queryable, inserts an immutable revision, and then
activates it. This applies to both custom Explorers and `default`. A failed
publication leaves the previous active revision in place and records
diagnostics on the failed revision.

On success, update the local store from the returned `state` rather than
manually inferring active metadata:

```json
{
  "project": "HTAN_INT-BForePC",
  "explorerId": "cohort-explorer",
  "publicationId": "interactive_...",
  "activeUrl": "/api/v1/projects/HTAN_INT-BForePC/explorers/cohort-explorer",
  "state": { "...ExplorerState...": "..." },
  "materializations": [],
  "diagnostics": []
}
```

## ExplorerConfig V2 rules

Every config sent to a custom Explorer endpoint must be a complete packet:

```json
{
  "apiVersion": "loom.calypr.org/explorer-config/v2",
  "kind": "ExplorerConfig",
  "project": "HTAN_INT-BForePC",
  "explorer": {
    "id": "cohort-explorer",
    "title": "Cohort Explorer",
    "management": "interactive"
  },
  "recipe": { "...valid Loom recipe bundle...": "..." },
  "views": [
    {
      "id": "document-reference",
      "title": "DocumentReference",
      "output": "DocumentReference",
      "table": {
        "columns": [
          {"column": "id", "label": "ID", "visible": true}
        ]
      }
    }
  ]
}
```

The `project`, `explorer.id`, and `explorer.management` values must match the
URL and operation. Interactive packets require at least one view. View output
names must exist in the recipe, and each view must have table columns.
Unknown fields are rejected.

The recipe is executable data, not a frontend AST. Presentation fields are
losslessly retained in the packet but are not reconstructed from the recipe.
Keep them in the config object when editing; do not rebuild a config from
`recipe` alone.

## Recommended frontend structure

Split the implementation into three layers:

1. `explorerApi` — typed REST calls and error decoding.
2. `explorerStore` — the selected project/Explorer, draft version/digest,
   active revision, and request status.
3. Presenters:
   - `DefaultExplorerPresenter`: renders `draftConfig` while editing, then
     `activeConfig` after publication, and derives views from `dataset.outputs`
     and `columns` when no complete packet is available.
   - `InteractiveExplorerPresenter`: renders `draftConfig.views` and validates
     referenced outputs/columns against live metadata.

The default presenter should tolerate missing optional presentation hints and
should drop a hint for a column that is no longer emitted. A stale custom
config should produce an actionable validation diagnostic and allow the user
to repair it; it should not be treated as the repository default.

The minimum request flow is:

```text
GET explorers
  -> GET selected Explorer
  -> edit draft (including default) and PUT /draft
  -> POST /authoring/compile when discovery changes
  -> POST /preview for the selected output
  -> POST /publish after explicit user confirmation
  -> replace local state with returned state
```

Do not route normal Builder operations through `/graphql/graph`. In particular,
remove dependencies on legacy types such as
`DataframeRecipeColumnCandidatesInput`; candidate discovery and compilation for
the Builder belong behind the REST lifecycle contract.

## Error handling checklist

- `400`: malformed request, missing expected version, unsupported limit, or
  missing required header/input. Show a request correction message.
- `403`: authorization failure.
- `404`: the Explorer does not exist. Refresh the Explorer list.
- `409` with `DRAFT_CONFLICT`: refresh server state and reconcile edits.
- `409` during publication/activation: keep the current active Explorer and
  show the returned diagnostic; do not claim publication succeeded.
- `422`: invalid V2 config, unresolved/incomplete catalog, unsupported output,
  materialization failure, or preview failure. Render diagnostics as terminal
  Builder feedback.
- `503`: publication capability is temporarily unavailable. Keep the draft
  local/server state intact and allow retry.

Never leave the UI in `Preparing preview` or `Publishing` after a terminal
HTTP response. Clear the pending state in both success and failure handlers.

## Migration checklist

- [ ] Add a configurable Loom REST base path.
- [ ] Replace GraphQL Explorer list/detail reads with the REST list/detail routes.
- [ ] Never require `default.draftConfig` for rendering.
- [ ] Render `default.activeConfig` when ETL supplied a complete packet.
- [ ] Generate the default layout from `dataset.outputs[].columns` when no
      complete `activeConfig` is available.
- [ ] Preserve complete custom V2 packets, including presentation fields.
- [ ] Send `expectedDraftVersion` on every draft/compile/publish mutation.
- [ ] Send the latest `expectedDraftDigest` when available.
- [ ] Handle `DRAFT_CONFLICT` by refreshing and reconciling, never overwriting.
- [ ] Replace GraphQL candidate input types with opaque REST candidate IDs.
- [ ] Use the REST compile result as the source of emitted columns and digests.
- [ ] Scope preview to one output and clear loading state on 4xx/5xx responses.
- [ ] Publish only after explicit confirmation and consume the returned state.
- [ ] Allow the repository default to use the same draft/compile/publish flow as custom Explorers.
