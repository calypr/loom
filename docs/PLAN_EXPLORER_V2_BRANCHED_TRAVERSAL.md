# Explorer V2 branched traversal authoring plan

## Implementation status (2026-08-27)

Implemented in Loom:

- the semantic V2 route tree remains the sole backend route contract;
- sibling and nested routes lower recursively and deterministically;
- columns remain bound to their exact authored occurrence;
- compile and preview are covered as non-active receipt operations;
- Publish remains the only activation operation; and
- Explorer authoring documentation now identifies editable route trees as MVP
  functionality and dynamic/pivot candidates as future work.

The coordinated frontend tree editor is implemented in IDP-Frontend. The
remaining activity is live BForePC Kubernetes acceptance and an intentional
test publication; it is an operational verification step, not missing route
contract code.

## Outcome

Make the existing Explorer V2 route-tree contract safe for an interactive
Builder that can add, select, and remove traversal branches. Loom remains the
authority for capability edges, route policy, deterministic compilation,
preview execution, and publication.

The primary implementation is in `IDP-Frontend`. Loom work is deliberately
limited to closing contract and test gaps around behavior that is already
substantially implemented.

The coordinated frontend plan is:

`IDP-Frontend/docs/PLAN_explorer-builder-branched-traversal.md`

## Product boundary

This work includes:

- a finite traversal tree rooted at each output's row resource;
- sibling and nested branches;
- repeated resources represented by distinct occurrence IDs;
- adding columns from ordinary primitive catalog fields at any occurrence;
- local branch removal and its dependent-column cleanup;
- compile and preview against non-active receipts; and
- publication as the only operation that changes the active Explorer.

This work does not include:

- pivot-family authoring;
- dynamic column discovery or expansion;
- browser-authored recipe expressions;
- V1 decoding or migration;
- automatic draft persistence; or
- a general graph-query language outside the V2 `RouteNode` tree.

## Current backend capability

The semantic V2 contract already has the required shape:

```text
RouteNode
  occurrenceId
  resourceType
  relationship
  children[]
```

Loom currently:

- validates the complete recursive tree and globally unique occurrence IDs;
- resolves every child through an exact capability relationship;
- applies maximum-depth, repeated-edge, and self-loop policies;
- sorts siblings by occurrence ID for deterministic lowering;
- attaches columns to exact occurrences;
- recursively emits sibling `recipe.Traversal` values;
- compiles immutable receipts without changing the active revision;
- previews exact receipts without changing the active revision; and
- materializes and switches the active revision only in Publish.

The existing BForePC V2 workspace is already a production-relevant branch
fixture. Its Specimen output has sibling Observation and Patient children, and
the Patient occurrence owns `HTAN_PARTICIPANT_ID`.

## Gaps

1. Explorer compilation tests cover nested routes but do not prove sibling
   branches with columns on both siblings.
2. HTTP lifecycle tests do not prove that a branched compile and preview leave
   the active revision unchanged.
3. There is no cross-system branch round trip covering repository workspace,
   Builder edit, compile, preview, publish, Builder reload, and Viewer output.
4. Row-grain behavior for two populated, repeated sibling branches is not
   asserted at the Explorer contract boundary.
5. Branch-specific route-policy errors are not covered with exact diagnostic
   paths.
6. Compatibility-only linear fields and helpers in `authoringv2.Document` can
   obscure which route model is authoritative. They must not be used by new
   branch code.

## Invariants

### Authoring identity

- `Document.Route` is the only durable route source.
- Every occurrence ID is globally unique within one document.
- Columns reference occurrences by exact occurrence ID.
- Sibling order does not change compilation identity.
- Snapshot-local catalog node, edge, and candidate IDs are never persisted as
  the semantic route.
- Relationship and resource type are persisted exactly as the V2 contract
  requires.

### Lifecycle

- Compile may persist an immutable receipt, but it must not save the workspace
  as a draft, create a revision, materialize an output, or switch an active
  pointer.
- Preview may execute a receipt, but it must not save a draft, create a
  revision, materialize a published output, or switch an active pointer.
- Publish is the only browser operation in this feature that may materialize
  and activate the edited workspace.
- A failed Publish retains the previously active Explorer revision.

### Query behavior

- Adding one sibling cannot change or remove another sibling.
- Removing one subtree removes only that subtree from the compiled recipe.
- Columns on retained occurrences preserve their names, labels, and
  presentation contracts.
- Optional sibling traversal lowering must preserve the declared root row
  grain. Any multiplicity behavior must be explicit in the output contract,
  not an accidental Cartesian expansion.

## Backend work packages

### B1. Canonical branch fixtures

Add a small semantic V2 fixture with this topology:

```text
ResearchSubject (base)
├── Patient (patient)
│   └── Condition (patient__condition)
└── Specimen (specimen)
```

Attach at least one primitive field column to every occurrence. Use readable
column and occurrence identities that exercise the non-root prefix contract.

Also load the BForePC Specimen route as a contract fixture where practical:

```text
Specimen (base)
├── Observation (observation)
└── Patient (patient)
```

### B2. Semantic compiler coverage

Add tests in `internal/explorer/compilation` proving:

- sibling branches lower to sibling recipe traversals;
- nested children remain attached to the correct sibling;
- columns bind to the correct occurrence and physical prefix;
- reversing authored sibling order produces the same deterministic recipe and
  receipt identity;
- duplicate occurrence IDs fail before lowering;
- ambiguous and unavailable relationships return exact route paths;
- maximum depth is evaluated per root-to-leaf path;
- repeated-edge and self-loop decisions match the advertised route policy; and
- removing a sibling changes only that sibling's recipe subtree and emissions.

Do not add a second route representation or translate through compatibility
`RouteSteps`.

### B3. Row-grain and physical execution coverage

Add an execution-level fixture with data on two sibling branches. Assert:

- the root output remains queryable;
- fields from both siblings appear under their frozen public names;
- missing optional children produce null/empty values according to the current
  traversal contract; and
- two populated repeated siblings do not create an undocumented root-row
  multiplication.

If this exposes a dataframer defect, fix the existing recursive traversal
lowering. Do not special-case Explorer routes in the renderer.

### B4. Receipt lifecycle coverage

Extend the native V2 HTTP contract tests to execute:

1. Load an Explorer with an existing active revision.
2. Compile a changed branched workspace.
3. Assert that the active revision, publication metadata, and Viewer contract
   are unchanged.
4. Preview the compiled receipt.
5. Assert again that active state is unchanged.
6. Publish the receipt.
7. Assert that materialization succeeds and the new branched revision becomes
   active atomically.
8. Reload Builder state and verify the exact route tree and occurrence IDs.

Also assert that a failed materialization or activation retains the prior
active revision.

### B5. Diagnostics and capability agreement

Verify that a frontend can decide legal child additions from one capability
snapshot and receive the same decision from compilation:

- exact source node;
- exact target node;
- exact relationship label;
- repeated-edge policy;
- self-loop policy; and
- maximum path depth.

Use existing stable authoring diagnostics. Add a new error code only if no
existing code describes a demonstrated failure. Do not add a branch feature
flag unless mixed-version deployment proves it necessary.

### B6. Documentation cleanup

Update `docs/EXPLORER_AUTHORING.md` to state that:

- ordinary catalog fields and editable route trees are part of the MVP;
- pivot/dynamic candidate families remain future work;
- semantic `RouteNode.children` is authoritative; and
- compile/preview receipts are non-active artifacts.

Mark compatibility-only linear route helpers as non-authoritative. Remove them
only when existing internal tests no longer require them; their removal is not
a prerequisite for shipping branches.

## Cross-system acceptance scenario

Use the checked-in BForePC workspace as the acceptance fixture:

1. Open the Specimen output.
2. Verify both Observation and Patient sibling occurrences are visible.
3. Select Patient and verify `HTAN_PARTICIPANT_ID` is editable.
4. Add one catalog-proven child under either existing occurrence.
5. Add an ordinary primitive field on the new occurrence.
6. Compile and preview successfully.
7. Verify the active Viewer still serves the old revision.
8. Remove or retain the new branch locally and preview again.
9. Publish the intended tree.
10. Reload Builder and Viewer and verify both sibling branches, labels, and
    queryable columns.

## Verification commands

At minimum, implementation should run:

```bash
go test ./internal/explorer/authoringv2 ./internal/explorer/compilation ./internal/server
```

Then run the existing Explorer conformance suite and the local authenticated
Kubernetes acceptance flow against the BForePC project.

## Delivery order

1. Land backend branch fixtures and failing coverage for the existing contract.
2. Implement the frontend tree model and interactions against that contract.
3. Run compile and preview acceptance without publication.
4. Run publication, reload, and Viewer acceptance.
5. Remove the frontend route-editing gate only when lossless branch hydration,
   mutation, and reconstruction tests pass.

## Exit criteria

- Loom compiles, previews, publishes, and reloads sibling and nested route
  branches without migration or fallback logic.
- Compile and Preview provably leave active Explorer state unchanged.
- Publish is the only operation that changes the active revision.
- BForePC Specimen retains both its Observation and Patient branches through a
  full Builder round trip.
- Viewer columns and labels from all retained occurrences match the frozen V2
  output contract.
