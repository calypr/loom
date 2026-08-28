# Backend-owned Explorer authoring plan

## Outcome

Move Explorer Builder authoring from a browser-owned document model to a
server-owned command model. The browser should describe a user's intent and
render the authoritative result. Loom should create durable identities, apply
defaults, enforce capability and workspace invariants, persist the canonical
draft, and compile that stored draft.

The first production milestone is deliberately narrow:

> Starting from a brand-new Explorer with no workspace, selecting an eligible
> catalog node creates a valid table, saves it, compiles it, and returns an
> immutable receipt without the browser constructing an output ID, tab ID,
> route root, or recipe-facing name.

This removes the current class of failure where `output.id` passes the public
authoring contract but fails later when Loom reuses it as
`recipe.outputs[].name`.

## Design principles

1. **Loom owns durable identity.** Output, tab, occurrence, column, emission,
   and recipe-facing identifiers are generated or derived by the backend.
2. **The browser sends intent, not canonical documents.** It may keep temporary
   component keys and optimistic visual state, but those values never become
   persisted identities.
3. **The saved workspace remains the source of truth.** Commands are an input
   protocol; they do not become a second durable authoring model.
4. **Every command is capability-scoped and concurrency-safe.** Structural
   commands use the exact snapshot token shown to the user, and every mutation
   uses draft version/digest compare-and-swap.
5. **Compilation remains receipt-based.** Commands do not weaken the existing
   immutable receipt, preview, or publication boundaries.
6. **Invalid intermediate states are prevented at the mutation boundary.** A
   client should not be able to construct a document Loom cannot lower.
7. **Migration is incremental.** Existing V2 workspaces, receipts, revisions,
   export, preview, and publication continue to work while the UI moves one
   operation at a time.

## Current boundary and failure mode

Today the browser submits a complete `Workspace` to `PUT /draft` and
`POST /compile` (with `POST /builder` as a compile alias). That makes the
browser responsible for assembling `Document`, `Output`, `Tab`, `RouteNode`,
`Column`, and their cross-references.

The contract currently accepts any nonblank `output.id`. Compilation copies
that value directly into a recipe output name, whose grammar is stricter:

```text
^[A-Za-z_][A-Za-z0-9_]*$
```

Consequently, a browser-created UUID, slug containing `-`, label containing a
space, or digit-prefixed value survives authoring validation and fails during
recipe lowering. Debouncing the request only delays the same failure.

The deeper issue is not just missing validation. A UI implementation should
not have to know recipe identifier rules in order to express “create a table
for this catalog node.”

## Target request flow

```text
Browser                       Loom
   |                            |
   | GET /builder               |
   |--------------------------->|
   | workspace:null + catalog   |
   | + draft version/digest     |
   |<---------------------------|
   |                            |
   | CREATE_TABLE intent        |
   | nodeId + title + CAS        |
   |--------------------------->|
   |                            | load/initialize workspace
   |                            | authorize snapshot
   |                            | generate output/tab IDs
   |                            | resolve node to root type
   |                            | validate + canonicalize
   |                            | save draft with CAS
   | authoritative workspace    |
   | + draft version/digest      |
   |<---------------------------|
   |                            |
   | reconcile stored draft     |
   |--------------------------->|
   | immutable receipt          |
   |<---------------------------|
```

The frontend still owns interaction and presentation decisions. It does not
own durable identities, canonical defaults, cross-document referential
integrity, or compiler-facing names.

## UI compatibility requirement

This migration must not redesign the Builder UI. Existing controls, selection
behavior, table layout, loading indicators, preview dialog, publish flow, and
diagnostic panel remain visually and behaviorally equivalent. The change is
below the component boundary:

```text
existing UI event
      |
      v
frontend Builder action/store
      |
      v
typed Loom command API
      |
      v
authoritative workspace response
      |
      v
existing UI view model
```

Do not make React components construct `Workspace`, `Document`, `Output`,
`Tab`, `RouteNode`, or `Column` wire objects. Components continue to report
events such as “root selected,” “candidate checked,” “column hidden,” and
“preview clicked.” A frontend integration layer maps those events to commands
and maps the returned workspace back into the existing view model.

Temporary component keys are allowed for rendering a pending optimistic row or
table. They are UI-only, must be clearly typed as temporary, and must be
replaced by the backend response. They must never be sent as `outputId`,
`tabId`, `occurrenceId`, or `column`.

## Frontend API migration

The frontend implementation target is the existing IDP frontend Builder. The
first frontend task is to identify its current API module, Builder store, and
event handlers; the migration should modify those seams rather than rewrite
components.

### Typed API client

Generate request/response types and runtime validators from the updated
Explorer authoring OpenAPI document. The Builder-facing API module should
expose this small surface:

```ts
interface ExplorerAuthoringApi {
  getCapability(project: string, explorerId: string): Promise<AuthoringCapability>;
  getBuilder(project: string, explorerId: string): Promise<BuilderState>;
  applyCommands(
    project: string,
    explorerId: string,
    request: ApplyCommandsRequest,
    signal?: AbortSignal,
  ): Promise<ApplyCommandsResponse>;
  reconcile(
    project: string,
    explorerId: string,
    request: ReconcileRequest,
    signal?: AbortSignal,
  ): Promise<CompileResponse>;
  preview(
    project: string,
    explorerId: string,
    request: PreviewRequest,
    signal?: AbortSignal,
  ): Promise<PreviewResponse>;
  publish(
    project: string,
    explorerId: string,
    request: PublishRequest,
    signal?: AbortSignal,
  ): Promise<PublicationResponse>;
}
```

Ordinary Builder code must stop calling `saveWorkspaceDraft`/`PUT /draft`,
`compileWorkspace`/`POST /builder`, or `POST /compile` with locally assembled
workspace content. Keep any whole-document calls in a separately named import
or compatibility module so a normal UI action cannot accidentally use them.

### Existing UI event to new call mapping

| Existing user action | Current client behavior to remove | New frontend call | Authoritative frontend update |
| --- | --- | --- | --- |
| Open Builder | GET Builder and sometimes fabricate an empty workspace | `GET /builder` | Store nullable workspace, catalog, snapshot token, draft version/digest |
| Click first/root node | Construct output/document/tab IDs and POST the workspace | `CREATE_TABLE {rootNodeId, title?}` | Replace workspace and select returned `outputId` |
| Add another table | Clone/build a document and tab locally | `CREATE_TABLE {rootNodeId, title?, afterOutputId?}` | Replace workspace; select returned table if current UX does so |
| Delete table | Splice documents/tabs and repair references locally | `DELETE_TABLE {outputId}` | Replace workspace and derive the next selected table from the response |
| Rename table | Patch document and tab titles locally | `RENAME_TABLE {outputId, title}` | Replace workspace while preserving current focus behavior |
| Reorder tables | Rewrite tab order locally | `REORDER_TABLES {outputIds}` | Render backend-normalized order |
| Change root | Rewrite root and clear dependent state locally | `SET_TABLE_ROOT {outputId, rootNodeId}` | Replace workspace and clear selections exactly as returned |
| Add route node | Build route/occurrence IDs locally | `ADD_ROUTE {outputId, parentOccurrenceId, edgeId}` | Expand/select the returned occurrence |
| Remove route node | Delete route subtree and dependent references locally | `REMOVE_ROUTE {outputId, occurrenceId}` | Replace route, columns, filters, and actions from the response |
| Check/add field | Construct a typed/physical column locally | `ADD_COLUMN {outputId, occurrenceId, candidateId, projectionMode?}` | Render the returned backend-created column |
| Uncheck/remove field | Splice column and dependent state locally | `REMOVE_COLUMN {outputId, columnId}` | Replace workspace from the response |
| Change visibility/order/label | Patch presentation object and autosave complete workspace | `UPDATE_COLUMN_PRESENTATION` (batchable) | Replace normalized presentation state |
| Autosave fires | PUT complete workspace | No separate call; every successful command is already persisted | Update saved indicator from returned draft version/digest |
| Preview clicked | Compile current local workspace if receipt is missing, then preview | Reconcile the current stored draft if needed, then `POST /preview` | Cache receipt by draft digest; render existing preview UI |
| Publish clicked | Compile local workspace if needed, then publish | Reconcile the current stored draft if needed, then `POST /publish {receiptId}` | Replace publication/active state from the response |
| Reload/reset | Rehydrate local document edits | `GET /builder` | Replace the Builder store with authoritative server state |

This table is the frontend work package. A backend command is not considered
delivered until the corresponding existing UI action uses it end to end.

### Frontend store state

The frontend store should hold server state and request coordination, not a
second canonical authoring document:

```ts
interface BuilderServerState {
  workspace: Workspace | null;
  catalog: CatalogSnapshot;
  snapshotToken: string;
  draftVersion: number;
  draftDigest: string;
  pendingCommands: PendingCommand[];
  receiptByDraftDigest: Record<string, CompileResponse>;
  lastAppliedCommandId?: string;
}
```

The workspace is replaced from successful server responses. Do not merge the
response into a locally constructed workspace field by field. Existing UI
selectors may continue to derive selected table, route tree, candidate state,
and presentation from this workspace.

Component selection state that is not durable—open panels, focus, search text,
scroll position, and temporary optimistic keys—remains local and is reconciled
using backend-returned identities.

### Command dispatch and ordering

Mutating commands for one Explorer must be serialized through one frontend
queue because each response advances the draft version and digest:

1. Read the latest `draftVersion`, `draftDigest`, and `snapshotToken` from the
   store when dispatch begins, not when a click handler is created.
2. Assign an ephemeral idempotency `commandId`. This is a request token, not a
   domain identity; the backend still creates every durable authoring ID.
3. Send the command with the current CAS values.
4. On success, atomically replace workspace/version/digest and apply the typed
   result to ephemeral selection/focus state.
5. Dispatch the next queued command using the returned CAS values.
6. Mark every cached receipt for an older draft digest as stale for preview and
   publication purposes; retaining it for request completion is harmless.

Presentation changes that currently debounce may be coalesced into one command
batch before dispatch. Structural commands should preserve user order and must
not be silently collapsed.

Reconcile requests may be canceled in the browser when a newer mutation is
queued. Loom may still finish and persist the old immutable receipt; the
frontend ignores it unless its `intentDigest` matches the current draft
digest. This preserves the existing responsive UI without relying on request
timing for correctness.

### Preview and publish orchestration

The UI-facing `preview()` action becomes:

```text
flush pending command queue
  -> find receipt for current draftDigest
  -> if absent, POST /reconcile for current version/digest
  -> verify returned intentDigest == current draftDigest
  -> POST /preview with receiptId + selected outputId + limit
  -> always clear the existing preview loading state
```

The UI-facing `publish()` action uses the same receipt check, then sends only
`{receiptId}` to the existing publish route. It must not publish while commands
are pending or use a receipt associated with an older draft digest.

This orchestration belongs in the store/action layer so existing Preview and
Publish components keep the same callbacks and visual states.

### Frontend error behavior

Use the existing Builder attention/diagnostic surface; this migration does not
require new screens.

- `DRAFT_CONFLICT`: stop the command queue, GET Builder, replace authoritative
  state, retain the rejected high-level command for an explicit retry, and
  clear pending indicators. Do not PUT a merged workspace.
- `STALE_CATALOG_SNAPSHOT`: refresh Builder/catalog state. Retry automatically
  only if the referenced node/edge/candidate still exists and the user action
  has not been superseded; otherwise retain the selection intent and show the
  existing diagnostic.
- `INVALID_COMMAND` or a command-specific `422`: leave authoritative workspace
  unchanged and bind the diagnostic to the existing control when a JSON path
  or entity ID is present.
- `503` or network failure: retain the queued intent and allow retry with the
  same `commandId`; do not generate a replacement domain ID or mutate the
  canonical workspace locally.
- Canceled reconcile/preview: clear loading state and suppress an error only
  when cancellation was caused by a newer local action.
- Older command/reconcile response: ignore it unless its returned draft
  version/digest still matches the current store.

### Capability negotiation and coordinated deployment

Extend `GET /capability` to advertise command and stored-draft reconcile
support explicitly, for example through `operations: [commands, reconcile]`
and command-family feature flags. The frontend must not discover support by
issuing a request and interpreting `404`.

Deploy in this order:

1. Backend ships schemas, command/reconcile routes, and capability flags with
   flags disabled or unavailable until ready.
2. Frontend ships the new API/store adapter but continues using the existing
   path when the advertised operation is absent during the migration window.
3. Enable the command path for coordinated environments and verify the shared
   contract suite.
4. Make commands mandatory for supported browser versions.
5. Remove the browser fallback only after deployment telemetry shows the
   supported backend fleet advertises the new operations.

Fallback is temporary compatibility, not route probing. A partially supported
command family must remain disabled in the UI adapter until every operation
needed by that existing UI workflow is advertised.

### Shared backend/frontend contract suite

Check request/response fixtures generated from the OpenAPI contract into a
location consumable by both repositories. At minimum, share fixtures for:

- nullable blank Builder state;
- successful `CREATE_TABLE` including backend-owned output/tab IDs;
- idempotent replay of the same create command;
- stale draft version conflict;
- stale snapshot rejection;
- add/remove column with backend-owned column identity;
- reconcile of an exact stored draft digest;
- ignored stale reconcile response; and
- preview/publish using the receipt for the current draft.

The backend runs producer contract tests. The frontend runs consumer tests
through its real API adapter and Builder store, plus its existing component
tests to prove the rendered controls and interactions did not change.

## Proposed additive API

Keep the existing V2 namespace and add an operation-oriented surface. The
workspace schema remains V2 and the new routes are additive.

### Read authoritative Builder state

Extend `GET /builder` so its response includes the CAS metadata required by
the next command:

```json
{
  "apiVersion": "loom.calypr.org/explorer-authoring/v2",
  "kind": "ExplorerBuilderState",
  "lifecycleState": "NEW",
  "workspace": null,
  "draftVersion": 0,
  "draftDigest": "",
  "catalog": {}
}
```

For a saved draft, return that draft's version and digest. When Builder falls
back to an active revision because no draft body exists, still return the
Explorer owner's current draft CAS values; the first command creates a new
draft from the active workspace against those values. The frontend must never
guess a version based on whether `workspace` is null.

### Apply commands

```http
POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/commands
Content-Type: application/json
```

```json
{
  "commandId": "01K...",
  "snapshotToken": "...",
  "expectedDraftVersion": 0,
  "expectedDraftDigest": "",
  "commands": [
    {
      "type": "CREATE_TABLE",
      "rootNodeId": "catalog-node-id",
      "title": "Patients"
    }
  ]
}
```

Response:

```json
{
  "commandId": "01K...",
  "workspace": {},
  "draftVersion": 1,
  "draftDigest": "sha256:...",
  "results": [
    {
      "type": "TABLE_CREATED",
      "outputId": "out_6d1936f0...",
      "tabId": "tab_6d1936f0..."
    }
  ],
  "diagnostics": []
}
```

Use an array so one user gesture can atomically express related changes. The
backend applies every command or none of them.

### Reconcile the stored draft

```http
POST /api/v1/projects/{project}/explorers/{explorerId}/authoring/v2/reconcile
Content-Type: application/json
```

```json
{
  "snapshotToken": "...",
  "draftVersion": 1,
  "draftDigest": "sha256:..."
}
```

Loom loads the referenced canonical draft and compiles it. The browser no
longer resubmits mutable workspace content to compile. The response is the
existing immutable compilation receipt response, optionally accompanied by
the authoritative workspace metadata used for compilation.

Preview and publish remain unchanged and continue to accept a receipt ID.

## Command model

Implement commands as a closed, tagged union in the OpenAPI schema and as a
pure authoring-domain reducer. Do not put HTTP, storage, recipe, or publication
logic in the reducer.

Initial command set:

| Command | Browser declares | Loom owns |
| --- | --- | --- |
| `CREATE_TABLE` | catalog root node and display title | workspace initialization, output ID, tab ID, root route, order, visibility, defaults |
| `DELETE_TABLE` | output ID | tab removal, shared-filter cleanup, ordering normalization |
| `RENAME_TABLE` | output ID and title | synchronized document/tab presentation |
| `REORDER_TABLES` | ordered output IDs | contiguous authoritative tab ordering |
| `SET_TABLE_ROOT` | output ID and catalog node ID | root resolution and clearing/reconciling invalid routes, columns, filters, and actions |
| `ADD_ROUTE` | output ID, parent occurrence ID, catalog edge ID | occurrence ID, relationship/resource resolution, route insertion |
| `REMOVE_ROUTE` | output ID and occurrence ID | subtree, dependent column/filter/action cleanup |
| `ADD_COLUMN` | output ID, occurrence ID, candidate ID, projection intent | typed `Column`, physical column name, label/type/capabilities, presentation defaults |
| `REMOVE_COLUMN` | output ID and column identity | dependent filters/actions/shared bindings cleanup |
| `UPDATE_COLUMN_PRESENTATION` | visibility/order/label/pinning/chart/filter intent | validation and normalized order |
| `SET_FIXED_FILTER` | output ID, column identity, values | typed validation and canonical filter replacement |
| `SET_ACTIONS` | output ID and action intent | valid column references and canonical action representation |

Start with `CREATE_TABLE`, `DELETE_TABLE`, `ADD_COLUMN`, and `REMOVE_COLUMN`.
They cover the first useful end-to-end Builder without prematurely designing
every presentation operation.

## Identity policy

Durable IDs must be opaque to the browser and stable across title changes.
They must not be derived only from user-visible labels.

For the first implementation, generate recipe-safe IDs directly:

```text
output:     out_<lowercase hex or base32>
tab:        tab_<lowercase hex or base32>
occurrence: occ_<lowercase hex or base32>   (root remains "base")
column:     col_<lowercase hex or base32>
```

The generator lives in `internal/explorer/authoringv2`, validates its own
output against the downstream recipe/physical-name grammar, and is covered by
property tests. The browser treats every returned ID as an opaque string.

Use `commandId` as an idempotency key. A retry after a lost response must
return the prior result rather than create a second table. The durable design
should store a small command result keyed by project, Explorer, and command ID.
A deterministic ID derived from that tuple can simplify the first vertical
slice, but it does not replace command replay records for non-create commands.

Longer term, consider separating opaque authoring identity from recipe-facing
physical names. That is not required for this migration and should not block
backend ownership.

## Backend structure

Add these boundaries:

```text
internal/explorer/authoringv2/
  command_types.go       closed command/result types
  command_apply.go       pure Apply(workspace, snapshot, commands)
  identity.go            server-owned ID generation
  command_errors.go      stable command diagnostics and paths

internal/explorer/
  service.go             orchestration: load, initialize, apply, CAS-save

internal/server/
  explorer_authoring_v2_routes.go
                         HTTP authorization, strict decoding, response mapping
```

The service operation should perform:

1. Load the Explorer identity and current draft.
2. If no draft exists, load the active workspace or initialize an empty
   workspace using server-owned Explorer metadata.
3. Resolve and authorize the exact capability snapshot token.
4. Apply all commands to an in-memory copy.
5. Validate and canonicalize the resulting workspace.
6. Save with expected version and digest.
7. Persist the idempotency result.
8. Return the stored workspace and its new version/digest.

The existing Arango draft write already performs compare-and-swap. Before
building commands on it, correct the `Store.SaveDraft` interface comment that
currently describes last-write-wins behavior and add adapter conformance tests
so every implementation has the same CAS semantics.

## Validation ownership

Even after the frontend stops creating IDs, close the current validation gap:

- define one reusable backend predicate for recipe-safe output identity;
- apply it in `Document.Validate`, not only after lowering;
- add the same pattern to the OpenAPI schema as documentation and generated
  client guidance, not as the source of truth;
- return an authoring diagnostic at
  `$.workspace.documents[n].output.id` rather than wrapping it only as
  `DOCUMENT_COMPILE_FAILED` at the document level; and
- audit tab, occurrence, and column identity constraints for the same kind of
  authoring/compiler mismatch.

Backend validation remains mandatory even when all known clients use commands.
Import, repository, migration, and older-client paths can still submit complete
documents during the transition.

## Concurrency and idempotency

- Require `expectedDraftVersion`; accept `expectedDraftDigest` as an additional
  guard.
- Return `409 DRAFT_CONFLICT` with the current version/digest when CAS fails.
- Do not silently apply a command to a different draft revision.
- Do not let the frontend retry by replacing the entire workspace.
- Make exact `commandId` replay return the original result.
- Reject reuse of one `commandId` with different command content.
- Keep command application atomic for command arrays.
- Add a bounded command-result retention policy only after published usage data
  exists; correctness comes before automatic cleanup.

Automatic semantic rebasing can be considered later for operations proven to
commute. It should not be part of the initial cutover.

## Compilation behavior

Do not merge mutable authoring and immutable execution concepts:

- commands mutate and save the draft;
- reconcile compiles one exact stored draft version into an immutable receipt;
- preview executes a receipt;
- publish materializes and activates a receipt.

The UI may debounce reconcile requests, but correctness must not depend on the
debounce. Concurrent reconciliations remain safe because receipts are
content-addressed. A reconcile request for a stale draft version returns a
conflict instead of compiling newer state than the user intended.

## Delivery phases

### Phase 0: close the contract hole

Backend:

- Add backend output-ID validation at the authoring boundary.
- Add the OpenAPI pattern and precise diagnostic path.
- Add regression tests showing that authoring validation and recipe validation
  accept and reject the same output identifiers.
- Add structured logging of command/type and diagnostic code, never complete
  workspace bodies.

Frontend:

- Identify and document the existing API module, Builder store, UI event
  handlers, and workspace-construction helpers that will be replaced.
- Add generated V2 types/runtime response validation and central error-code
  decoding without changing components.
- Add characterization tests for the current UI interactions and rendered
  states so the call migration can prove visual/behavioral equivalence.

This improves correctness and diagnosis but does not by itself fix new-table
creation in the current frontend.

### Phase 1: blank Explorer to server-created table

Backend:

- Add command types, pure reducer, identity generator, and command endpoint.
- Extend `GET /builder` with authoritative draft version/digest and advertise
  the new operations through `GET /capability`.
- Implement empty-workspace initialization and `CREATE_TABLE`.
- Resolve `rootNodeId` only through the supplied capability snapshot and
  require `RowRootEligible`.
- Create the output, root route, and tab together.
- Save through draft CAS and return the authoritative workspace.
- Add `POST /reconcile` to compile a stored draft version.

Frontend:

- Add `applyCommands` and `reconcile` to the typed API client.
- Route the existing first-node-click action through `CREATE_TABLE`; remove
  output/tab/document construction from that handler.
- Replace the Builder store workspace/version/digest from the command response
  and map the returned output ID into the existing selected-table state.
- Route the existing debounced compile action through stored-draft reconcile.
- Preserve the current loading, success, and Builder-attention UI states.
- Run the shared blank-state/create/reconcile fixtures against a coordinated
  backend deployment.

Exit criterion: the exact blank-config scenario that motivated this plan works
without any frontend-generated durable ID.

### Phase 2: useful table construction

Backend:

- Implement `DELETE_TABLE`, `ADD_COLUMN`, and `REMOVE_COLUMN`.
- Resolve catalog candidates into complete typed columns on the backend.
- Generate physical column identities and compiler-owned defaults on the
  backend.
- Cover compile, preview, export, reload, and publish for the resulting table.

Frontend:

- Route the existing table-delete and candidate checkbox handlers through
  commands without changing their components.
- Remove local typed-column generation and dependent-reference repair.
- Add the serialized command queue and receipt-by-draft-digest cache.
- Change existing Preview and Publish callbacks to flush commands, reconcile
  when needed, then call the unchanged receipt endpoints.
- Verify existing preview/publish dialogs and status indicators are unchanged.

Exit criterion: a user can create, populate, preview, save, reload, and publish
a single table using commands only.

### Phase 3: routes and multi-table workspaces

Backend:

- Implement route add/remove and root replacement with dependency cleanup.
- Implement table reorder/rename and multiple output handling.
- Ensure shared-filter bindings and tab order remain valid after every command.
- Add repeated-edge, self-loop, and route-subtree tests.

Frontend:

- Route existing root, route, rename, reorder, create-table, and delete-table
  events through their commands.
- Remove occurrence-ID generation, route-tree mutation, tab-order repair, and
  cross-table reference cleanup from frontend helpers.
- Preserve expansion, focus, selected table, and route-node selection as
  ephemeral UI state keyed by backend-returned identities.

Exit criterion: all structural Builder actions use server-owned commands.

### Phase 4: presentation, filters, and actions

Backend:

- Move presentation normalization, fixed filters, shared filters, charts, and
  file actions behind typed commands.
- Batch high-frequency presentation changes where appropriate.

Frontend:

- Route existing presentation/filter/chart/action controls through command
  batches while retaining their current debounce behavior.
- Keep local optimistic UI state disposable and reconcile it against every
  authoritative response.
- Delete the remaining ordinary-edit whole-workspace serializer and autosave
  call path after the command coverage matrix is complete.

Exit criterion: the browser no longer constructs or patches canonical
workspace objects.

### Phase 5: compatibility retirement

Backend and operations:

- Measure use of full-workspace `PUT /draft`, `POST /compile`, and the
  `POST /builder` compile alias by client version and caller class.
- Retain full-document import/export and repository ingestion as explicit
  trusted workflows.
- Mark browser-oriented whole-workspace writes deprecated.
- Remove them only after supported frontends use commands and stored artifacts
  have passed migration/audit checks.

Frontend:

- Remove the temporary legacy browser fallback and capability branch.
- Keep whole-document import/export in an explicitly separate workflow.
- Verify production bundles contain no ordinary Builder call site for
  `PUT /draft`, workspace-bearing `POST /builder`, or `POST /compile`.

## Test strategy

### Domain tests

- Every command either produces a fully valid workspace or leaves the input
  unchanged.
- Generated identities always satisfy downstream grammars.
- Commands cannot reference stale outputs, occurrences, candidates, or edges.
- Removal commands clean every dependent reference.
- Reordering always produces contiguous stable order.
- Applying a command batch is atomic.

### Service and storage tests

- Empty, draft-backed, and active-revision-backed initialization paths.
- Version and digest conflict behavior across all store adapters.
- Exact command replay after a lost response.
- Command-ID reuse with different content.
- Concurrent commands from two tabs.
- No partial draft write when validation or persistence fails.

### HTTP contract tests

- Strict tagged-union decoding and unknown-field rejection.
- Authorization and capability-token checks.
- Stable diagnostic code, stage, and JSON path for every rejected command.
- Generated OpenAPI client models match the hand-written domain types.

### End-to-end tests

At minimum, automate these paths:

1. blank Explorer -> select root -> server-created table -> reconcile;
2. add column -> reconcile -> preview;
3. reload -> authoritative IDs unchanged;
4. publish -> active revision -> viewer;
5. lost command response -> retry -> exactly one table;
6. two tabs mutate the same draft -> one succeeds, one receives a conflict;
7. snapshot becomes stale between read and command -> explicit stale-snapshot
   response with no draft mutation; and
8. old valid V2 workspace -> command mutation -> reconcile -> publish.

## Observability and rollout

Record counters and latency for:

- command type and outcome;
- validation, capability, conflict, and persistence failures;
- command replay hits;
- draft load/apply/validate/save durations;
- reconcile requests by draft version;
- legacy whole-workspace writes by client version; and
- frontend fallbacks to the legacy path.

Roll out the command capability behind an advertised authoring feature. The
frontend selects the command path only when the server advertises it. Start
with internal projects, then new Explorers, then existing drafts. Do not infer
support from route failure.

## Completion criteria

The migration is complete when:

- no supported browser generates or persists durable authoring IDs;
- no supported browser submits a complete workspace for ordinary editing;
- all browser structural edits are server-applied, capability-validated, and
  CAS-persisted;
- compilation reads a specific stored draft revision instead of accepting
  browser-supplied workspace content;
- existing Builder components and user-visible workflows remain unchanged,
  with characterization/component tests proving the API/store cutover rather
  than a UI rewrite;
- blank-to-first-table, retry, conflict, reload, preview, and publication flows
  have end-to-end coverage;
- legacy whole-document endpoints are limited to explicit import,
  repository/ETL, or compatibility workflows; and
- telemetry shows no supported-client use of the deprecated browser write path
  for one full release window.

## Explicit non-goals

- Changing immutable receipt, preview, or publication semantics.
- Persisting recipe expressions, AQL, physical collection names, or
  authorization predicates in browser intent.
- Automatic conflict merging in the first release.
- Replacing the canonical workspace with an event log.
- Removing V2 import/export or repository-authored configuration.
