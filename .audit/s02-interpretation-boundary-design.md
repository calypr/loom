# Server-owned configured-column context

## Problem

The Builder currently reconstructs FHIR source identity in TypeScript. `interpretationCandidate.ts` compares paths, code systems, codes, and extension URL paths with catalog data. `InterpretationPanel.tsx` then repeats Go's applicability and rule selection. Go already owns the authoritative fail-closed resolution in `internal/explorer/compilation/interpretation.go`. S02 closes only when the browser stops making those decisions.

## Usage

The Builder loads one context for the exact authorized snapshot and saved draft:

```ts
const context = await client.resolveConfiguredColumnContexts({
  project,
  explorerId,
  snapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
});
```

`ColumnSelector` joins a saved `(outputId, column)` to opaque candidate IDs. `InterpretationPanel` joins the same locator to server-selected applicable revision IDs and display-only library summaries. Neither component reads a FHIR path, coding system, code, extension URL, structural match, rule, or source definition to establish identity.

Creating a reusable mapping names the saved column rather than submitting a client-built rule:

```ts
await client.createInterpretationRevisionFromColumn({
  project,
  explorerId,
  snapshotToken,
  expectedDraftVersion,
  expectedDraftDigest,
  outputId,
  column,
  libraryId,
  parentRevisionId,
  explanation,
});
```

Existing preview receipts and `APPLY_INTERPRETATION_CANDIDATE` remain unchanged.

## Shape

Add one bulk read under authoring v2 and one authoring-scoped create mutation. Keep the manual project-level rule-create API compatible for non-Builder clients.

The bulk read returns:

- the echoed snapshot token, draft version, and draft digest;
- display-only immutable library-head and pinned-revision summaries;
- one entry per configured column with `outputId`, `column`, `occurrenceId`, opaque candidate IDs, and a closed interpretation-resolution result;
- only `READY` results carry applicable revision IDs;
- `MISSING`, `AMBIGUOUS`, and `UNSUPPORTED` carry a display reason and no semantic payload.

Go exposes one pure shared resolver in `internal/explorer/compilation`. Both pinned compilation and the new context projection call it. The resolver owns capability candidate matching, concept matching, FHIR paths, coding identity, extension ancestry, and the structural candidate. Lifecycle owns authorization, snapshot/draft freshness, bounded repository reads, library-head applicability, and response projection.

Stale snapshots, changed authorization scope, invalid routes, and draft mismatches fail the whole request. A valid current column whose source cannot resolve uniquely receives a per-column unavailable result. Legacy sources remain visible and editable; the server does not guess a mapping or rewrite the draft.

The create mutation reloads the exact saved column, rejects pinned columns, calls the shared resolver, derives applicability and the one immutable rule in Go, then delegates to the existing revision preparation and parent compare-and-swap repository operation.

The read is bounded. It rejects workspaces or library sets above explicit limits, deduplicates pinned revision IDs, batch-loads heads where the repository permits it, indexes revisions before the column loop, and reports a limit error instead of silently truncating applicability.

## Ownership

| Module | Responsibility |
|---|---|
| `internal/explorer/compilation/interpretation.go` | Pure, authoritative configured-column resolution shared with pinned compilation. |
| `internal/explorer/lifecycle/interpretation_context.go` | Exact workspace/snapshot checks, bounded revision loading, applicability selection, generic projection. |
| `internal/explorer/lifecycle/interpretation_library.go` | Create a revision from an exact saved column and retain parent CAS. |
| OpenAPI and server adapters | Closed request/response DTOs and read/write authorization. |
| `api.ts` and `interpretation.ts` | Runtime validation and generic DTO access only. |
| `BuilderWorkspace`, `ColumnSelector`, `InterpretationPanel` | Index by opaque IDs, render summaries, and send user selections. |

Delete `authoring/interpretationCandidate.ts` after both callers migrate. Do not add a second lifecycle matcher or include domain revision/rule/source structs in the configured-column context DTO.

## Synthesis decision

Candidate A is the base. Both independent candidates converged on a bulk context read and target-based create mutation. Candidate A won because its transport model does not leak full interpretation revisions, rules, applicability, or source definitions. Candidate B contributed explicit `occurrenceId`, whole-request failures for stale authorization and route state, deduplicated repository loading, and the verification matrix. The cross-judge required three corrections now included above: reject pinned-column creation, impose explicit work bounds, and encode resolution as a sum type.

Persisting snapshot-derived candidate IDs on draft columns was rejected because schema and authorization changes can stale them. Extending the one-column source inspector was rejected because it would produce request fan-out and mix diagnostic presentation with semantic resolution.

## Verification

- Resolver agreement tests cover fields, coded values, extensions, ambiguous sources, and legacy normalization.
- Lifecycle tests cover multiple columns, exact project/scope, stale draft and snapshot conflicts, invalid routes, bounded work, pinned revision loading, read-only behavior, and parent conflicts.
- UI tests cover opaque candidate lookup, unavailable states, display-only applicable heads, server-derived create, preview/cancel/apply, and no automatic application after create.
- OpenAPI generation/checks and all affected Go and UI suites pass.
- The live Builder creates and revises a mapping, previews and applies the exact receipt, reloads the pin, and rejects stale context/create requests without mutation.

## Implementation reconciliation

No deviations accepted yet. Implementation begins with the shared Go resolver and agreement tests.
