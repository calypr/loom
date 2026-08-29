# Explorer compilation architecture

Loom treats the Builder as a mutable editor over a server-owned compiler. Each
accepted editor state produces one immutable, disposable compilation receipt.
The frontend chooses only capability IDs and presentation intent; it never
constructs recipe selectors, graph storage details, physical plans, or AQL.

This is the implemented Goal D boundary. The public authoring surface is V2.
V1 remains only for offline migration of stored legacy documents.

## Production path

```text
V2 Builder document + exact snapshot token
  │
  ▼
active-generation and read-scope authorization
  │  exact project, generation, scope digest, schema digest
  ▼
native V2 translator
  │  route occurrences, candidates, projection modes, stable emissions
  ▼
resolved recipe compilation
  │  semantic validation and deterministic physical lowering
  │  no catalog discovery: the capability already proved every operation
  ▼
immutable compilation receipt
  │  insert-ignore, content validation, tenant-scoped readback
  ├──────────────────────────────┐
  ▼                              ▼
receipt preview                  receipt publication
  │                              │
  │ deterministic re-lowering    │ deterministic re-lowering
  │ preview LIMIT                │ full streaming materialization
  ▼                              ▼
scoped Arango query              ClickHouse outputs
                                 │
                                 ▼
                          release and revision activation
```

The HTTP endpoints are:

- `GET .../authoring/v2/capability`
- `POST .../authoring/v2/builder` for direct workspace compilation
- `POST .../authoring/v2/reconcile` for compilation of the exact persisted draft
- `POST .../authoring/v2/preview` with `receiptId`, `outputId`, and an optional
  limit
- `POST .../authoring/v2/publish` with `receiptId`

Compile-before-preview is therefore safe. A frontend may debounce compile
while editing and may compile once more immediately before preview if its
latest editor state has no receipt.

## Native V2 translation

`internal/explorer/compilation` translates `authoringv2.Document` directly to
`recipe.Bundle`. Production does not translate V2 to V1 and then translate V1
to a recipe.

The translator validates all identities against the exact immutable capability
snapshot. It supports:

- zero-hop routes;
- every finite route allowed by the snapshot policy;
- repeated edges and self-loops when advertised;
- repeated resource types at distinct route occurrences;
- occurrence-specific candidate selection;
- compiler-proven projection modes; and
- deterministic public column, emission, traversal alias, and recipe IDs.

Semantic Builder outputs use `traversalColumnNaming: ALIAS`. Builder occurrence
IDs are globally unique namespaces, so a nested column keeps its authored
`occurrence__column` name instead of acquiring ancestor prefixes during
physical lowering. General recipes retain the backwards-compatible `PATH`
default and continue to compose every traversal alias.

An invalid route, stale candidate, ambiguous occurrence, unsupported projection
mode, or invalid presentation reference is returned as a structured error with
stage, code, JSON path, message, and details. These are authoring errors, not
raw recipe, AQL, or Arango errors.

The translator is persistence-neutral and does no catalog reads, field
profiling, graph probing, HTTP work, or database writes. Its only authority is
the supplied capability snapshot.

## What a receipt means

A current receipt means:

> Loom accepted this exact V2 document against this exact active capability
> and authorization scope, produced this resolved semantic recipe and public
> output contract, and can execute it later without interpreting the authoring
> document or discovering schema again.

The receipt pins:

- receipt-format and compiler-contract versions;
- project and Explorer identity;
- canonical V2 intent and its digest;
- capability token, source generation, capability schema digest, and exact
  authorization-scope digest;
- idempotent compilation key;
- resolved recipe and recipe/schema digests;
- compiler-owned Explorer presentation config;
- public output contract and its digest;
- candidate/occurrence/emission mappings;
- emitted public columns;
- canonical per-output execution fingerprints;
- durable explicit/discovered column provenance used by publication; and
- deterministic warnings.

Request ID and creation time are operational metadata and do not affect the
receipt ID. Receipt IDs are content-addressed. Reads validate the stored ID,
compilation key, resolved-recipe digest, and output-contract digest before the
artifact can execute.

Execution fingerprints cover the unoptimized renderer-neutral operation graph,
bind values, row identity, dynamic validation behavior, and ordered output
schema. They exclude optimizer reports and decisions, optimized plans,
diagnostic counters, source/debug provenance, preview limits, output selectors,
and publication-only authorization projections.

The receipt deliberately does **not** persist:

- optimized physical IR;
- rendered AQL;
- bind variables;
- physical collection or table names; or
- request authorization credentials.

Those objects are implementation details of the current request. Persisting
them would couple durable Builder artifacts to an optimizer, renderer, or
storage layout. Deterministic re-lowering of a frozen resolved recipe is cheap
and is not semantic recompilation.

## Idempotency and persistence

The compilation key covers the canonical intent, snapshot, scope, generation,
schema, receipt format, and compiler contract. It is an idempotency identity,
not evidence that a previously stored artifact remains reproducible. Production
therefore rebuilds normalized intent on every explicit compile/recompile.

The result is validated, then persisted with immutable
`INSERT ... OPTIONS { overwriteMode: "ignore" }` semantics. Loom reads and
re-lowers the authoritative stored document with the same verifier used by
preview before returning success. A compile never returns 200 with an artifact
that preview would immediately reject. Contract failures identify the mismatched
component without exposing plans, bind values, or authorization paths. The Arango collection
has a composite index over project, Explorer, compilation key, receipt format,
and compiler contract. Receipt lookup for execution is always scoped by
project, Explorer, and receipt ID.

There is no correctness dependency on an in-process cache. Same-key
singleflight is permitted, but Kubernetes resources, `GOMEMLIMIT`, and
`GOMAXPROCS` own workload limits. Receipt statistics expose count, approximate
serialized bytes, oldest creation time, and unreferenced count. Loom retains
explicit orphan purge but does not impose an automatic TTL until real storage
measurements justify one.

The compiler records verified compile duration, serialized receipt bytes,
output count, and public column count. Performance objectives must be measured
against the complete rebuild-and-verify path and tenant-scoped receipt lookup.

The pure native translator benchmark uses a 20-hop, 100-selection document and
reports time and allocations. It is not a wall-clock CI assertion; service
latency must be measured against the deployed Arango and authorization stack.

## Preview

Preview performs these steps:

1. tenant-scoped receipt lookup and receipt integrity validation;
2. re-authorization against the receipt's exact retained generation;
3. equality checks for project, generation, authorization-scope digest, and
   capability schema digest;
4. requested-output validation;
5. deterministic semantic/physical lowering of the stored resolved recipe;
6. equality checks for resolved recipe/schema digests and output fingerprints;
7. application of the preview limit; and
8. parameterized AQL rendering and execution.

It does not decode V2 intent, migrate through V1, query field profiles, resolve
catalog candidates, or widen authorization. A restricted-empty scope remains
restricted-empty. Preview also does not request the publication-only
`auth_resource_path` output column.

Preview safety limits are part of this execution contract: every request has
a ten-second server deadline and a 32 MiB encoded-response cap. The response
is atomic; Loom returns no partial rows when the cap is exceeded. A static plan
gate rejects plans above the approved preview class before database execution,
and representative generated plans are checked with Arango `EXPLAIN`. Passing
the static gate does not guarantee success when Arango is unavailable.

The stable preview failure codes are `PLAN_TOO_EXPENSIVE`,
`RECEIPT_STORE_UNAVAILABLE`, `PREVIEW_TIMEOUT`,
`PREVIEW_RESPONSE_TOO_LARGE`, `CLIENT_CANCELED`, and
`BACKEND_UNAVAILABLE`. Receipt-store failures and timeouts are retryable;
response-size and plan-cost failures require a smaller request. Client
cancellation is not a server outage. Fiber does not reliably distinguish a
browser disconnect from every other canceled request on all HTTP paths, so
the adapter must preserve the typed cancellation when available and must not
classify an empty or truncated response by inspecting error text.

Inactive generations may be previewed while their immutable data and capability
snapshot are retained and the caller still has the exact effective scope.

## Publish

Publish starts from the same receipt and repeats the integrity and exact-scope
checks. It additionally requires the source generation to remain active and
requires at least one selected public output column. An empty intermediate
editor state can be compiled, but publishing it returns `NO_SELECTED_COLUMNS`.

The resolved recipe is deterministically lowered and its digests/fingerprints
are checked before rows are streamed to publication storage. Publication adds
the authorization provenance column required by ClickHouse. Loom verifies that
all expected outputs are queryable, activates the dataset release, atomically
stores the immutable Explorer revision and receipt, and then switches the
active Explorer pointer. Failure before that final transition leaves the prior
active revision intact.

The compiled revision retains the canonical V2 authoring document. Builder
reads decode V2 directly; only genuinely old revisions use the V1 migration
decoder.

## Compatibility and invalidation

Existing published revisions remain readable. Existing artifactless or
old-contract receipts are not lazily upgraded during preview or publish,
because that would reinterpret mutable authoring intent at an execution
boundary. They return `RECEIPT_RECOMPILE_REQUIRED` and must be explicitly
compiled again.

Compiler-contract changes, capability/schema changes, generation changes,
authorization-scope changes, or intent changes produce a different compilation
key and receipt. Physical optimizer changes do not require rewriting receipts;
optimizer state is outside the canonical execution fingerprint. Runtime
lowering uses the current optimizer after verifying the frozen unoptimized
execution contract.

## Implementation map

| Responsibility | Implementation |
| --- | --- |
| V2 protocol and canonical intent | `internal/explorer/authoringv2` |
| Compiler-proven immutable capability | `internal/explorer/capability` and `internal/server/explorer_capability.go` |
| Native V2 translation | `internal/explorer/compilation` |
| Versioned receipt domain and integrity | `internal/explorer/compilation_receipt.go` |
| Memory and Arango receipt persistence | `internal/explorer/memory_store.go`, `internal/explorer/arango/store.go` |
| Explorer list/create/authoring/receipt/preview/publication workflows | `internal/explorer/lifecycle` |
| Resolved-recipe execution boundary | `internal/dataframe/recipe/engine/engine.go` |
| Generated HTTP request/response adapters | `internal/server/openapi_explorer_routes.go` |
| Production lifecycle composition and execution adapters | `internal/server/explorer_lifecycle_adapter.go` |
| Compiler-owned output/config validation policy | `internal/explorer/lifecycle/policy.go` |
