# Frontend Dataframe Facet Performance Plan

## Purpose

Use this document as the implementation plan in the frontend Codex workspace.
It is intentionally framework-neutral: the implementing agent should first
locate the existing Explorer table hooks, GraphQL documents, generated types,
and request-cache library, then map the steps below onto those components.

The goal is to make the Explorer table feel fast without changing Loom's
GraphQL schema or inventing another endpoint.

## Current problem and measured baseline

The current table render eagerly requests 156 aliased `dataframeAggregate`
fields, including distributions for identifiers and reference-like columns.
It also sends dataset metadata, table rows, scalar counts, and facet
distributions as separate GraphQL operations.

Measurements from the local Loom deployment on 2026-08-18:

- The 156-facet operation takes approximately 1.05-1.22 seconds.
- It is already one ClickHouse statement, not 156 statements.
- Federation/project metadata takes approximately 653-669 ms.
- Aggregate planning, ClickHouse execution, and result decoding take
  approximately 374-559 ms.
- A separate one-job count takes approximately 666 ms end-to-end even when
  ClickHouse takes only 5 ms, demonstrating that repeated metadata resolution
  is a major cost.
- Dataset, count, facet, and row requests have different request IDs, so Loom's
  request-scoped project/federation cache cannot be shared across them.

The 156 aliases are not themselves the bug. They are GraphQL response names.
The remaining problems are eager overfetching, unbounded legacy facet results,
and multiple HTTP operations for one logical table render.

## Desired user experience

1. Initial navigation renders the table, count, and immediately visible filter
   controls from one GraphQL operation.
2. The page does not calculate distributions for every dataset column.
3. A facet is loaded only when it is visible, explicitly configured for eager
   display, or opened by the user.
4. Facet menus return a bounded number of terms and clearly represent missing
   and truncated results.
5. Reopening an unchanged facet is instant from the frontend cache.
6. Filter, sort, pagination, and publication changes cannot display stale
   results from an older request.

## Frozen backend contract

Do not require Loom schema or route changes. Continue using:

- `POST /loom/graphql/graph` (or the frontend's configured Loom GraphQL base).
- `dataframeRows(input: DataframeRowsInput!)`.
- `dataframeAggregate(input: DataframeAggregateInput!)` where a distinct
  scalar count is genuinely required.
- `dataframeAggregations(input: DataframeAggregationsInput!)` for bounded
  `TERMS` facets.

The exact selector remains:

```graphql
input DataframeSelectorInput {
  recipe: String!
  translationVersion: String!
  output: String!
}
```

Never send a physical table name or infer a selector from a display label.

## Target request architecture

### Initial table render

Replace separate dataset, rows, filtered-count, unfiltered-count, and eager
facet operations with one operation wherever their selector and filter state
are the same.

Use this representative shape and adapt field names to the frontend's query
generator:

```graphql
query ExplorerTableRender(
  $rows: DataframeRowsInput!
  $facets: DataframeAggregationsInput!
) {
  table: dataframeRows(input: $rows) {
    materialization {
      id
      name
      revision
      rowCount
      availability
      completeness
      columns {
        name
        logicalType
        nullable
        repeated
        filterable
        sortable
        aggregatable
      }
    }
    columns
    rows
    totalCount
    pageInfo {
      hasNextPage
      endCursor
    }
  }

  facets: dataframeAggregations(input: $facets) {
    materialization {
      id
      revision
    }
    aggregations
  }
}
```

The rows and facets variables must use the same selector and current filter
snapshot. Do not make the dataset metadata request separately: the
`dataframeRows.materialization` object already provides the column and
revision metadata needed by the table.

Use `dataframeRows.totalCount` as the filtered row count. Do not issue another
ungrouped `COUNT` for the same filters. Add an aliased `dataframeAggregate`
field to this same operation only when the UI displays a genuinely different
count scope that cannot be derived from `totalCount` or materialization
metadata.

If there are no eager facets, use a rows-only version of the document. Do not
send an empty `dataframeAggregations.specs` list because Loom rejects it.

### Bounded facet specifications

Represent facet menus with `TERMS`, not legacy unbounded `COUNT + groupBy`:

```json
{
  "selector": {
    "recipe": "calypr-meta-default",
    "translationVersion": "...",
    "output": "Patient"
  },
  "filters": [],
  "specs": [
    {
      "name": "facet__project_id",
      "kind": "TERMS",
      "column": "project_id",
      "size": 50,
      "excludeSelfFilter": true
    }
  ]
}
```

Rules:

- Default `size` to 50; make it configurable per filter and cap normal UI use
  at 100.
- Use stable, collision-free spec names derived from the public column name.
- Use `excludeSelfFilter: true` for a column's own filter menu so selected
  values remain visible while other active filters still constrain counts.
- Consume `missingCount` and `truncated` from the returned aggregation JSON.
- When `truncated` is true, label the list as partial and offer text entry or
  another refinement mechanism. Do not imply that every distinct value was
  returned.
- A `dataframeAggregations` field supports at most 50 specifications. Normal
  initial rendering should remain well below that. If a deliberate bulk load
  exceeds 50, split it deterministically into multiple aliased
  `dataframeAggregations` fields in the same GraphQL operation.

## Facet demand policy

Do not derive eager facets from every column where `aggregatable == true`.
That flag means the operation is legal, not that it is useful at page load.

Define one explicit frontend policy function, for example:

```text
facetMode(column, explorerConfig) -> eager | lazy | none
```

Recommended policy:

- `eager`: explicitly configured filter controls that are visible without user
  interaction, such as project, status, category, or small enumerations.
- `lazy`: useful categorical/date/number filters whose menus are collapsed at
  initial render.
- `none`: row identifiers, subject/patient identifiers, opaque identifiers,
  free text, URLs, resource references, attachment paths, and other fields
  whose distributions are high-cardinality or not meaningful as a menu.

Prefer explicit Explorer configuration over name heuristics. A conservative
name heuristic may be used only as a fallback and must be overridable. In
particular, do not eagerly facet columns ending in `_id`, identifier values,
or `*_reference` merely because the backend says they are aggregatable.

Set an initial safety budget, such as at most 12 eager facets. Additional
facets become lazy even if configured eager, with a development warning so a
configuration author can correct the layout.

## Lazy facet loading

When a facet control opens or becomes visible:

1. Capture one immutable snapshot of selector and filters.
2. Remove only that facet's own predicates when `excludeSelfFilter` semantics
   are desired; retain every other predicate.
3. Submit a bounded `dataframeAggregations` request containing that facet and
   any other newly requested facets collected during a short batching window
   (approximately one animation frame or 10-25 ms).
4. Deduplicate identical in-flight requests.
5. Cancel obsolete network requests with `AbortController` or the equivalent
   supported by the existing GraphQL client.
6. Apply a response only if its selector, filter signature, and
   materialization revision still match current table state.

Do not fire one request per facet when several controls mount together. Batch
newly demanded facets into one `dataframeAggregations` field, up to 50 specs.

## Frontend cache model

Cache each facet independently even when several arrive in one response.

The cache key must include:

```text
selector recipe/version/output
+ materialization revision
+ public column name
+ aggregation kind and size
+ canonical effective filters after self-filter exclusion
```

Canonical filter rules:

- Trim column and operation names; uppercase operations.
- Sort the final filter list because filters are combined with `AND`.
- Sort members of `IN`, `NOT_IN`, and `ARRAY_OVERLAPS` by stable JSON because
  member order is not semantic.
- Preserve duplicate predicates.
- Use stable JSON encoding for values.

The materialization revision makes published data immutable for the key. When
the revision changes, old entries may remain in the cache but must no longer
be selected. Apply a bounded LRU/TTL policy to prevent unlimited growth.

Suggested freshness behavior:

- Fresh for the current revision during the browser session.
- Revalidate after a configurable interval if the existing client library
  requires it.
- Never reuse across different principals unless the application's existing
  cache is already scoped to the authenticated session.

## State management and race safety

Create one table-render input model containing:

- exact selector;
- selected row columns;
- canonical filters;
- sort;
- pagination cursor/page size;
- demanded facet columns; and
- materialization revision once known.

Derive GraphQL variables from that model in one place. Do not let rows, count,
and facets independently read mutable filter state during the same render.

On filter or sort changes:

- reset the row cursor;
- preserve cached facets whose effective filter signature did not change;
- invalidate/refetch affected facets;
- cancel stale table-render requests; and
- retain the last successful rows/facets according to the product's existing
  loading-state convention, rather than flashing an empty table unnecessarily.

On selector or publication revision changes, invalidate the complete logical
table view.

## Implementation sequence for the frontend Codex agent

### 1. Discover the current request graph

Search for:

- `dataframeAggregate`, `dataframeAggregations`, and `dataframeRows` GraphQL
  documents or query builders;
- the code that assigns aliases such as `a0` through `a155`;
- hooks/stores for dataset metadata, total count, rows, and facets;
- Explorer column/filter configuration types;
- the GraphQL client's request deduplication and cancellation support; and
- current performance telemetry and request-ID handling.

Document which component triggers each current HTTP operation before editing.

### 2. Add pure policy and canonicalization utilities

Implement and unit-test:

- facet mode selection (`eager`, `lazy`, `none`);
- eager facet budget enforcement;
- stable facet spec naming;
- canonical filter signatures; and
- independent facet cache keys.

Keep these functions framework-independent.

### 3. Introduce one table-render query builder

Build one GraphQL operation containing rows, materialization metadata, and
bounded eager facets. Reuse `rows.totalCount` and remove redundant count and
dataset operations for the same state.

If the frontend uses generated GraphQL artifacts, regenerate them using the
repository's normal generator. Do not hand-edit generated files.

### 4. Replace eager all-column facet loading

Stop mapping every dataset column to a legacy aliased aggregate. Request only
the bounded eager set on initial render. Mark remaining eligible controls lazy.

### 5. Implement lazy batched facet loading

Add open/visibility-driven loading, the 10-25 ms batching window, request
deduplication, cancellation, and revision/filter race guards.

### 6. Add cache integration

Store normalized results per facet key. Ensure results returned together can
be read and invalidated independently.

### 7. Instrument and compare

Record at least:

- initial GraphQL POST count;
- initial requested facet count;
- total GraphQL request bytes and response bytes;
- time to first table rows;
- time until eager filters are usable;
- lazy facet cold and cache-hit latency;
- canceled/stale request count; and
- GraphQL error codes grouped by operation.

Use browser performance marks and the existing telemetry layer. Preserve Loom
request IDs when available so browser measurements can be correlated with
backend logs.

## Tests

### Unit tests

- Identifier/reference/free-text columns are not eager by default.
- Explicit configuration can opt a valid column into or out of faceting.
- The eager budget is deterministic.
- Equivalent filter order and `IN` member order produce the same cache key.
- Different selector, revision, column, size, or effective filter produces a
  different key.
- Self-filter exclusion removes only predicates for the facet's own column.
- Duplicate filters are retained.
- Facet response normalization preserves missing counts and truncation.

### Component/integration tests

- Initial table mount sends one GraphQL operation containing rows and eager
  facets.
- No initial request contains all 156 dataset columns.
- No redundant dataset or same-filter count operation is sent.
- Opening one lazy facet requests only that facet.
- Opening several controls together produces one batched request.
- Reopening an unchanged facet uses cache without a network request.
- Changing an unrelated filter refetches the facet with the correct effective
  filters.
- Changing only a facet's self-filter does not fragment its cache when
  self-filter exclusion makes the effective predicate set identical.
- A response from an older filter/revision snapshot is ignored.
- Pagination changes rows without refetching unaffected facets.
- Partial/truncated results are visibly distinguished from a complete list.
- GraphQL alias-specific errors do not erase successful rows or other facets.

### End-to-end test

Use the `HTAN_INT-BForePC` Patient selector and a schema with approximately 156
columns:

1. Load the Explorer table.
2. Assert one initial `/loom/graphql/graph` POST.
3. Assert the initial facet spec count is at most the configured eager budget.
4. Assert rows and count render correctly.
5. Open a lazy categorical facet and verify its values against the legacy
   implementation.
6. Open a high-cardinality identifier filter and verify it does not preload an
   unbounded distribution.
7. Apply and remove filters, paginate, and change sort while checking for stale
   responses.
8. Capture performance and payload metrics for comparison with the baseline.

## Acceptance criteria

- Initial render uses one GraphQL POST for rows, row materialization metadata,
  filtered total count, and eager facets sharing one selector/filter snapshot.
- Initial render never eagerly requests all 156 facets.
- Initial eager facets are bounded to 12 by default, or another explicitly
  documented product limit no greater than 20.
- Every facet request uses bounded `TERMS` with `size <= 100` unless a reviewed
  use case explicitly requires another aggregation kind.
- Identifier/reference/free-text distributions are lazy or disabled by
  default.
- Opening multiple facets in the same render interval produces one batched
  request, not one request per facet.
- Cache keys include selector, revision, column, size, and canonical effective
  filters.
- Stale requests cannot overwrite newer table state.
- Requested facet values, counts, missing counts, filters, pagination, and
  sorting match the existing UI for the same bounded result set.
- Browser tests show at least a 75% reduction in initially requested logical
  facet jobs and at least a 50% reduction in GraphQL HTTP operations for one
  table render.
- Record p50/p95 time-to-first-rows before and after. The change is accepted
  only if time-to-first-rows improves materially without moving the delay to a
  blocking loading state elsewhere in the UI.

## Non-goals

- Do not change Loom's GraphQL schema or generated backend files.
- Do not add a frontend-specific Loom endpoint.
- Do not replace aliases with 156 separate HTTP requests.
- Do not preload every facet into a global cache.
- Do not claim a truncated `TERMS` response is the complete domain.
- Do not make physical ClickHouse table names part of frontend state.

## Separate backend follow-up

This frontend work removes unnecessary demand and allows Loom's existing
request-scoped cache to work as designed. A separate Loom task should then:

1. make active project releases the authoritative source-selection path;
2. remove the legacy source-discovery fallback after existing projects are
   migrated to active releases;
3. batch active-release/execution metadata lookup instead of resolving projects
   sequentially; and
4. consider a bounded release-revision-aware metadata/result cache.

That backend follow-up targets the remaining approximately 650 ms metadata
cost. It should not block the frontend changes in this plan.

## Definition of done handoff

The frontend pull request should include:

- a short before/after request diagram;
- the old and new initial GraphQL POST counts;
- old and new eager facet job counts;
- representative browser performance traces;
- unit, component, and end-to-end coverage described above; and
- a note confirming that Loom routes and schemas were unchanged.
