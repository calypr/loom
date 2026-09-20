# S02 route contract decision

## Decision

Use one server-issued construction choice for one exact source construction:

`authorized snapshot + row root + ordered route + terminal source + output forms`

The browser submits only the existing `choiceId` and selected `form`. It does
not submit an inferred path. Applying a related choice reauthorizes and
reproves the complete route and terminal source, then reuses or materializes
that route and adds the column in the existing atomic workspace transaction.

## Ownership

- The capability layer enumerates bounded, directed route alternatives from
  the immutable authorized snapshot. It reports truncation and never equates
  shortest with intended meaning.
- The compiler capability layer proves the complete route and terminal source
  together.
- Lifecycle issues and re-resolves the opaque choice, rejects stale context,
  and passes a typed resolved route into the reducer.
- The existing authoring workspace remains the only durable route and column
  store. The reducer reuses an exact route prefix or creates a sibling branch.
- The graph remains the explicit advanced editor. Explicit routes are preserved
  as overrides and catalog additions never rewrite them.
- The UI displays route meaning and multiplicity, but does not interpret FHIR
  relationships or decide route validity.
- Go also owns source presentation. Construction choices and the lazy saved-
  column provenance read expose generic summaries and label/value facts. React
  validates and renders that presentation contract; it does not derive FHIR
  owner, code, value, unit, or extension semantics from the persisted source
  union.

## Contract invariants

1. Zero-hop root choices and related choices use the same construction model.
2. Route identity includes ordered capability edge IDs, endpoint identities,
   relationship labels, storage directions, and match modes.
3. A choice is pinned to snapshot, authorization context, semantic inventory
   context when applicable, row root, route, and terminal source.
4. Apply never substitutes a different path for a stale or invalid route.
5. Multiple meaningful routes remain separate choices with plain-language
   route and multiplicity descriptions.
6. Bounded discovery exposes `complete`, `truncated`, and a cursor. A truncated
   search cannot claim that no route or one unique route exists.
7. The complete route and selected output must compile before a choice is
   advertised and again before it is applied.
8. Any failed item in a command batch leaves both routes and columns unchanged.

## Rejected alternative

A separate route-selection token plus a route-independent construction token
was rejected. It creates two independently stale identities for one column,
requires a new combination command, and makes it possible for source and route
authority to drift. Its proposed shortest-extension rule also conflicts with
the product requirement to preserve distinct route meanings.

## Migration

New choices use a route-bound token version. Existing root-only choice tokens
remain readable during the UI rollout. Existing route nodes without exact edge
identity continue through unique semantic resolution; ambiguous legacy routes
require repair instead of arbitrary selection. Both the catalog and starting
collection flows must move off `catalogPaths.ts` before that helper is deleted.

## Required proof

- inbound and outbound related choices;
- a complete five-edge route and terminal-source proof;
- distinct alternatives to the same resource type;
- exact prefix reuse and sibling preservation;
- stale snapshot, scope, semantic context, edge, source, and form rejection;
- honest limit/truncation behavior;
- atomic rollback;
- reload, preview, trace, and export source-tuple parity between automatic and
  explicitly graph-authored routes.
