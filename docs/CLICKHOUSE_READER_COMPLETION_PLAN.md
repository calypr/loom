# ClickHouse Reader Completion Plan

## Purpose

This plan closes the first three unfinished Loom reader items identified in
`CLICKHOUSE_GRAPHQL_READER_EXECUTION_PLAN.md`:

1. Explorer facet, histogram, and statistics parity;
2. bounded ClickHouse-backed streaming downloads;
3. integration fixtures that exercise the reader against a real ClickHouse
   instance.

The core publication catalog, authorized `dataType` federation, row reads,
basic aggregates, and GraphQL transport already exist. This work extends that
reader; it does not redesign publication or add a second read backend.

## Implementation status

The current implementation now includes the shared native ClickHouse streaming
boundary, principal-scoped dataframe export endpoint, batched terms/missing/
numeric-histogram/date-histogram/statistics aggregation contract, flat GraphQL
transport, unit coverage, and an opt-in real-ClickHouse reader fixture wired
into CI. Full nested aggregation trees, recursive filter AST input, and
calendar/timezone-specific date bucketing remain follow-up parity work within
the broader facet contract.

Deployment cutover evidence and Gecko frontend implementation are outside this
plan. Loom will provide stable contracts and fixtures that those later tasks can
consume.

## Current gaps

### Aggregations

`internal/dataframe/materialization` currently executes one aggregate per
request and supports `COUNT`, `COUNT_DISTINCT`, `SUM`, `AVG`, `MIN`, and `MAX`.
It does not provide a batched facet operation, missing buckets, numeric/date
histograms, combined statistics, nested terms, deterministic bucket limits, or
self-filter exclusion.

### Downloads

The existing generation export streams raw Arango documents as NDJSON. It is
not a published-dataframe export and does not share the ClickHouse reader's
`dataType`, authorization, selected-column, filter, or sort semantics. The
ClickHouse client also exposes only a buffered row-query method.

### Integration coverage

The repository has an opt-in native-driver round-trip smoke test. It does not
exercise publication resolution, authorized federation, row pagination,
facets, exports, or the HTTP/GraphQL surfaces against ClickHouse.

## Design constraints

- Resolve only current, READY, catalog-registered outputs.
- Derive the project/source set from the authenticated principal; requests do
  not accept a project selector.
- Apply server-derived row authorization in every federated source branch.
- Validate all selected, filtered, sorted, grouped, and aggregated columns
  against publication metadata.
- Bind values and intervals through the ClickHouse driver. Only validated
  identifiers may appear in generated SQL.
- Use one canonical filter and sort compiler for rows, aggregations, and
  exports so the three paths cannot drift.
- Keep GraphQL for interactive reads. Large response bodies use an HTTP
  streaming endpoint.
- Enforce limits before or during ClickHouse execution, not after buffering a
  complete result.

## Workstream 0: shared reader query plan

This is the prerequisite for facets and downloads.

1. Extract source-union construction, authorization predicates, selected
   column validation, filter compilation, and sort compilation from the
   current row and aggregate execution methods into an internal query-plan
   layer under `internal/dataframe/materialization`.
2. Represent the resolved dataset revision and ordered physical sources in the
   plan. Requests continue to refer only to public `dataType` and column names.
3. Extend the ClickHouse boundary with an iterator/callback streaming method
   that scans rows without building `[]map[string]any` in memory. Keep the
   existing buffered method for interactive GraphQL responses.
4. Add typed capability validation so invalid filter, histogram, and aggregate
   combinations fail before execution.
5. Add shared configurable limits for selected columns, filter depth/nodes,
   `IN` values, sort columns, aggregation specs, buckets, execution time,
   export rows, and export bytes.

Exit criteria:

- Row reads retain their current public behavior.
- Rows, aggregations, and exports consume the same resolved source plan and
  authorization predicates.
- Unit tests prove that unknown identifiers and unsupported type/operator
  combinations fail before a ClickHouse call.
- A canceled request cancels the underlying ClickHouse query.

## Workstream 1: facet, histogram, and statistics parity

### Contract

Add a batched `dataframeAggregations` GraphQL operation. One request contains a
`dataType`, the canonical filter expression, and a bounded list of named
aggregation specifications. Supported specifications are:

- `TERMS`: value/count buckets, missing count, limit, and deterministic
  count-then-value or value ordering;
- `HISTOGRAM`: numeric interval and optional bounds;
- `DATE_HISTOGRAM`: calendar or fixed interval using an explicit timezone;
- `STATS`: count, missing count, distinct count, minimum, maximum, sum, and
  average where valid for the column type;
- `MISSING`: missing-value count;
- bounded terms-with-terms or terms-with-missing sub-aggregations needed by
  Explorer.

Each specification has a stable name, typed result kind, and normalized JSON
result. Do not reproduce Guppy's generated GraphQL types or response nesting in
Loom.

`excludeSelfFilter` removes predicates targeting the aggregation's own public
field from the canonical filter expression while preserving the rest of the
expression. This behavior belongs in the Loom aggregation request so every
client gets identical semantics; Gecko may reshape only the returned result.

### Implementation

1. Add aggregation request/result models and limits to
   `internal/dataframe/materialization`.
2. Compile all named aggregation specifications from one resolved federated
   dataset. Coalesce compatible specifications into as few ClickHouse queries
   as practical, without making query generation opaque or untestable.
3. Define missing as SQL `NULL`. Empty strings and empty arrays remain values
   unless the public contract explicitly requests different handling.
4. Define numeric bucket boundaries as half-open intervals, with the final
   bounded bucket also half-open. Return the bucket key and count explicitly.
5. Define date bucket keys as RFC 3339 timestamps and reject ambiguous or
   unsupported intervals before execution.
6. Apply per-specification and total bucket limits. Truncation is explicit in
   result metadata; it must not silently look complete.
7. Add the GraphQL schema, resolver adapter, stable validation/error codes, and
   complexity accounting.
8. Preserve the existing singular `dataframeAggregate` operation for current
   internal callers until they migrate; implement it through the new aggregate
   engine where practical.

### Tests

- Terms ordering, limits, null/missing values, Unicode, booleans, and arrays.
- Numeric histograms with negatives, zero, boundaries, and sparse ranges.
- Date/DateTime histograms with timezone and daylight-saving boundaries.
- Stats for signed/unsigned integers, floats, nullable values, and empty sets.
- Self-filter exclusion in nested `AND`, `OR`, and `NOT` expressions.
- Nested terms/missing sub-aggregations and total bucket-limit rejection.
- Multi-project federation with different row authorization scopes.
- Unsupported column types, invalid intervals, excessive limits, and SQL
  injection attempts.

Exit criteria:

- Enum, range, numeric/date chart, statistics, missing, nested, and self-filter
  fixtures return the normalized expected results.
- Every aggregation uses the same authorized source set and row predicates as
  the corresponding table query.
- High-cardinality requests cannot produce unbounded work or responses.

## Workstream 2: ClickHouse streaming export

### Contract

Add `POST /loom/api/v1/dataframe/export` with an authenticated JSON body containing:

```json
{
  "dataType": "file",
  "columns": ["id", "file_name", "file_size"],
  "filter": null,
  "sort": [{"column": "id", "direction": "ASC", "nulls": "LAST"}],
  "format": "CSV",
  "fileName": "files.csv"
}
```

Initially support `CSV`, `TSV`, `JSON`, and `JSONL`. Manifest-oriented export
is a named projection/profile over the same endpoint, not a separate bypass of
the reader contract.

The response sets the correct `Content-Type`, a sanitized
`Content-Disposition`, and the request ID. Errors before the first byte use the
normal JSON error envelope. Errors after streaming starts are logged with the
request ID and terminate the response.

### Implementation

1. Resolve the principal-scoped `dataType` and build the shared reader query
   plan used by interactive rows.
2. Validate public columns, filter, and sort before sending response headers.
3. Add a streaming ClickHouse row iterator/callback that preserves native
   values and promptly closes rows on completion, error, timeout, or client
   disconnect.
4. Implement format writers:
   - CSV and TSV write one header followed by escaped records;
   - JSON writes a valid streamed array without retaining prior rows;
   - JSONL writes one object per line.
5. Keep deterministic ordering. If no public sort is supplied, use the same
   stable internal source/row ordering as interactive reads.
6. Enforce row, byte, duration, and concurrent-export limits. Record whether a
   limit, timeout, cancellation, or backend error ended the stream.
7. Emit structured metrics/logs for dataset revision, selected columns, query
   digest, rows, bytes, duration, completion status, and request ID. Do not log
   unrestricted row values.
8. Keep the existing raw generation export route unchanged; rename internal
   interfaces if needed so raw Arango export and dataframe export cannot be
   confused.

### Tests

- CSV/TSV quoting, delimiters, newlines, nulls, arrays, and Unicode.
- JSON validity for zero, one, and many rows; JSONL record boundaries.
- Exact row/column/filter/sort equivalence with an interactive reader query.
- Restricted and unrestricted principals across multiple project sources.
- Invalid format, field, filter, filename, and oversized request rejection
  before headers are committed.
- Row/byte/time/concurrency limit behavior and client cancellation.
- A large fixture proving bounded server memory rather than a buffered result.

Exit criteria:

- Every supported format contains exactly the authorized rows selected by the
  equivalent interactive query, in the same deterministic order.
- Export memory use is bounded by driver and encoder buffers rather than total
  result size.
- Cancellation and configured limits stop ClickHouse work.

## Workstream 3: real ClickHouse integration fixtures

### Harness

1. Keep unit tests hermetic. Mark real-ClickHouse tests with a build tag or the
   existing `LOOM_CLICKHOUSE_URL` opt-in and document one canonical local
   command.
2. Add a CI job with a pinned ClickHouse service matching the supported
   production major/minor line. Run the same suite locally against
   `experimental/docker-compose.yml`.
3. Create fixture helpers that:
   - create a unique database or uniquely prefixed tables per test run;
   - publish registered READY outputs through the real materialization store;
   - create multiple projects and generations for one alias;
   - clean up only resources bearing the test-run prefix.
4. Store small, reviewable input rows and normalized expected results under a
   dedicated `testdata` directory. Fixtures must not require a live Guppy or
   legacy backend.

### Required integration scenarios

- Publication to immediate dataset discovery without server restart.
- Authorized multi-project federation and unauthorized source non-disclosure.
- Restricted row scopes applied independently in each source branch.
- Scalar, nullable, array, Unicode, date/time, and numeric-boundary decoding.
- Ascending/descending keyset pagination with duplicate and null sort values.
- Terms, missing, numeric/date histogram, stats, nested, and self-filter
  aggregation fixtures.
- CSV, TSV, JSON, and JSONL export equivalence with interactive rows.
- Pointer advancement/federation revision behavior and stale cursor rejection.
- Missing physical tables and catalog/schema drift produce stable errors.
- Query cancellation and row/byte/time limit enforcement.

### Test layers

1. **Store layer:** native DDL, insert, streaming scan, cancellation, and type
   decoding.
2. **Reader layer:** real SQL over registered federated materializations,
   including authorization, pagination, aggregations, and limits.
3. **Transport layer:** GraphQL aggregation requests and HTTP streamed exports
   through `httptest`, backed by the same real ClickHouse fixtures.
4. **Parity layer:** static captured inputs and normalized expected outputs for
   the initial Explorer facet and download cases.

Exit criteria:

- The CI suite provisions ClickHouse and cannot pass by skipping all
  integration tests.
- Failures identify the fixture, reader operation, and expected/actual
  normalized result.
- The existing native-driver smoke test is either absorbed into the harness or
  retained only as a smaller diagnostic test.
- Unit SQL-generation tests remain, but reader completion does not depend on
  them as a substitute for execution.

## Delivery order

1. Land the shared query-plan and streaming ClickHouse boundary with regression
   tests for current row reads.
2. Land the real-ClickHouse harness and baseline row/federation fixtures early,
   so subsequent work is verified against actual execution.
3. Implement batched aggregations together with their unit and real fixtures.
4. Implement streaming export and format/limit fixtures.
5. Add GraphQL/HTTP transport integration coverage and static Explorer parity
   cases.
6. Update `CLICKHOUSE_GRAPHQL_READER_EXECUTION_PLAN.md` status only after all
   exit criteria above pass in CI.

## Definition of done

These three reader items are complete when:

- Loom exposes bounded terms, missing, histogram, statistics, nested, and
  self-filter aggregation behavior over the authorized federated dataset;
- Loom streams CSV, TSV, JSON, and JSONL from ClickHouse using the same dataset,
  authorization, filter, column, and sort contract as interactive reads;
- the reader, aggregation, and export paths run in CI against a real ClickHouse
  instance with deterministic fixtures covering federation, authorization,
  types, pagination, limits, and error cases;
- no completion claim relies solely on generated-SQL assertions, Docker Compose
  availability, or an opt-in driver smoke test.
