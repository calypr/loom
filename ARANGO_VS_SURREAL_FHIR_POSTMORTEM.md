# ArangoDB vs SurrealDB for FHIR Graph Dataframing

Evidence convention in this document:

- Repository implementation claims link to files in this repo.
- Database architecture claims link to official docs or upstream database source.
- Runtime numbers are labeled as attached-run or conversation-captured evidence
  unless they come from code. They should be rerun with
  [`Benchmark`](internal/proto/benchmark.go) before external publication.

## Executive Summary

This prototype tested whether ArangoDB and SurrealDB can support the same
realistic FHIR graph/dataframe workload:

1. load reduced GDC-style FHIR NDJSON through
   [`Load`](internal/proto/load.go),
2. preserve every source resource as raw JSON payload through the row-builder
   path in [`row_builder.go`](internal/proto/row_builder.go),
3. extract FHIR references into the shared `fhir_edge` collection defined in
   [`backend.go`](internal/proto/backend.go),
4. build a populated field catalog through the load-time profiler path in
   [`field_catalog.go`](internal/proto/field_catalog.go), and
5. produce a patient-first `gdc_case_assay_matrix` dataframe through backend
   query files selected by [`DefaultCaseAssayQueryPathForBackend`](internal/proto/query.go).

Both databases can store the logical model because the backend abstraction uses
the same collection/table contract for resources, `fhir_edge`,
`fhir_field_catalog`, and `patient_file_rollup` in
[`bootstrapSpec`](internal/proto/backend.go). Both can ingest the reduced
dataset once the loader uses backend-native write paths: Arango routes batches
through its store client in [`internal/store/arango/client.go`](internal/store/arango/client.go),
and Surreal routes relation edges through its relation-specific insert path in
[`internal/store/surreal/client.go`](internal/store/surreal/client.go). The
meaningful difference appeared at read time: the dataframe query is a
high-fanout graph expansion plus nested JSON classification problem, as shown in
the Arango query [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
and the Surreal query [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

The recommendation is therefore workload-specific:

**Use ArangoDB for the live FHIR dataframe builder. Treat SurrealDB as a research
backend unless the product accepts a prepared/materialized dataframe layer.**

This is not because SurrealDB lacks graph features. Its source code shows real
graph-specific storage and execution machinery: `RELATE` writes graph adjacency
keys in [`key/graph/mod.rs`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/key/graph/mod.rs#L1-L36)
and [`Document::store_edges_data`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs#L71-L128),
and SurrealDB has a
[`GraphEdgeScan`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L60-L107)
execution operator. The difference is
that this workload needs repeated adjacency expansion, deduplication, payload
extraction, aggregation, and row streaming over millions of edges. ArangoDB's
AQL traversal executor, implemented upstream in
[`TraversalExecutor.cpp`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp),
matched that shape more directly in this prototype, as shown by the AQL traversal clauses in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
and the Surreal prepare/materialization fallback in
[`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go).

The main differentiator is how each database moves through connected data:

- ArangoDB is traversal-first for this workload. Edges are part of the graph
  execution path in AQL traversal clauses such as `INBOUND ... fhir_edge` in
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql),
  so high-fanout adjacency expansion can stay inside one traversal-oriented AQL
  plan.
- SurrealDB stores relation records and graph adjacency keys, and can scan those
  keys. In this workload, broad graph expansion still became a sequence of graph
  scans, relation/target hydration, array construction, and downstream
  filtering before a dataframe row could be emitted; the fallback implementation
  materializes that rollup in [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go).

That difference is why the load summaries showed matching logical counts while
dataframe generation diverged; the benchmark harness records these phases
separately in [`BenchmarkSummary`](internal/proto/benchmark.go).

## The Workload Being Tested

The benchmark is not a generic graph benchmark. It is a specific FHIR dataframe
workload.

The loaded data has these properties, all reflected in the loader and bootstrap
contract:

- vertices are FHIR resources such as `Patient`, `Specimen`, `Group`,
  `DocumentReference`, `Condition`, `ResearchSubject`, and `Observation`; the
  resource types are discovered from `META/*.ndjson` in [`Load`](internal/proto/load.go);
- source payloads remain nested FHIR JSON under `payload`; this is the row
  contract emitted by [`row_builder.go`](internal/proto/row_builder.go) and
  consumed by the AQL and SurrealQL dataframe queries;
- graph edges are derived from FHIR references and stored in `fhir_edge`, which
  is the shared `EdgeCollection` in [`backend.go`](internal/proto/backend.go);
- every vertex and edge is scoped by `project`, with project indexes defined in
  [`bootstrapSpec`](internal/proto/backend.go);
- there are far more edges than vertices in the benchmarked reduced dataset, as
  shown by load summaries recorded in the benchmark section below;
- dataframe queries start from `Patient` and traverse to downstream clinical and
  assay evidence, as shown by the patient driver in
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
  and [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

The logical storage contract is shared in
[`internal/proto/backend.go`](internal/proto/backend.go):

- resource collection/table per FHIR resource type,
- `fhir_edge` for reference edges,
- `fhir_field_catalog` for observed populated fields,
- `patient_file_rollup` for the SurrealDB prepared read path.

The case/assay dataframe has to do more than "walk a graph." For each patient,
the Arango and Surreal query files require these operations:

- read patient identifiers/extensions from nested FHIR arrays in
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
  and [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql);
- find related specimens, groups, and files through `fhir_edge` traversals or
  prepared rollups in those same query files;
- hydrate `DocumentReference` payloads and classify file
  categories/types/experimental strategies/workflows in those same query files;
- summarize assay availability flags and counts in those same query files;
- include diagnosis, study, and treatment metadata in those same query files.

That query shape is the center of the comparison.

## Core Differentiator: Traversal Logic, Fan-Out, Payload Hydration, and Streaming

The `gdc_case_assay_matrix` workload is closer to graph ETL than to a point
lookup. It turns a connected FHIR graph into one wide row per patient. The
critical operation is not "find one related record." It is high-fanout adjacency
expansion followed by target-record hydration and nested FHIR payload
classification. The conclusion below is workload-specific and comes after
looking at both traversal implementations.

### ArangoDB Traversal Path

ArangoDB's graph traversal is represented in AQL as a traversal clause, but the
important detail is that this syntax is lowered into a traversal-specific
executor. In upstream ArangoDB,
[`TraversalExecutorInfos::parseTraversalEnumeratorSingleServer`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L261-L300)
constructs a `TraversalEnumerator` backed by a `SingleServerProvider`, while
[`TraversalExecutorInfos::parseTraversalEnumeratorCluster`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L305-L365)
constructs the cluster provider variant and, in the enterprise build path,
selects SmartGraph handling. The executor is therefore not treating traversal as
a generic document join; it creates a graph traversal enumerator configured with
order, uniqueness, weighting, provider options, and path validation options.

The runtime loop is also traversal-specific. Before a search starts,
[`TraversalExecutor::initTraverser`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L480-L533)
extracts the start vertex id, prepares traversal index expressions, and resets
the enumerator to that source vertex. Once running,
[`TraversalExecutor::produceRows`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L431-L450)
fills output rows by repeatedly calling
[`TraversalExecutor::doOutput`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L385-L429).
`doOutput` calls `traversalEnumerator()->getNextPath()` and directly writes the
requested vertex, edge, and path registers into the AQL output row.

That implementation shape maps closely to this repo's Arango dataframe query.
The query starts from `Patient`, caches one-hop neighbors with
[`FOR neighbor, edge IN 1..1 INBOUND p fhir_edge`](queries/gdc_case_assay_matrix_arango_rows.aql),
then continues with more inbound traversal clauses for
[`specimen -> group`](queries/gdc_case_assay_matrix_arango_rows.aql) and
[`specimen/group -> DocumentReference`](queries/gdc_case_assay_matrix_arango_rows.aql).
Because the traversed vertex is already an AQL variable, the same query can
enter payload classification immediately with
[`FOR doc_ref IN files LET doc = doc_ref.payload`](queries/gdc_case_assay_matrix_arango_rows.aql).
The graph expansion and payload projection are therefore expressed as one AQL row
pipeline in this implementation.

### SurrealDB Traversal Path

SurrealDB also has real graph-specific machinery. Its parser turns graph syntax
such as `->`, `<-`, and `<->` into graph lookup parts in
[`parse_remaining_idiom`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/syn/parser/idiom.rs#L106-L136).
Its relation persistence layer stores graph adjacency keys when relation records
are created. The graph key module states that each `RELATE` writes four KV keys
that model a relation between the left vertex, the edge record, and the right
vertex in
[`key/graph/mod.rs`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/key/graph/mod.rs#L1-L36).
The document edge persistence path writes those left/right pointer and inner
edge keys in
[`Document::store_edges_data`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs#L71-L128)
and then resets the canonical `in` and `out` endpoint fields so user document
content cannot override relation endpoints in
[`Document::store_edges_data`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs#L129-L133).

SurrealDB's execution engine also has a graph-specific scan operator. The
[`GraphEdgeScan`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L60-L107)
type takes a child input operator, a traversal direction, edge table specs, an
output mode, and optional target-table filtering. Its source comment describes
the execution model as a streaming DAG where `CurrentValueSource` feeds one or
more `GraphEdgeScan` operators. This is important: Surreal is not doing naive
full table scans for every graph hop, and the graph syntax is not fake.

The detailed execution path is source-record driven. In
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L193-L260),
the operator establishes database context, permission checks, child-stream
buffering, traversal direction, output mode, scan batch size, and whether full
records must be fetched. It then extracts record ids from incoming child batches
and, for each source record id, computes graph key ranges with
[`compute_graph_ranges`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L547-L580).
Those ranges are built from graph key prefixes/suffixes or table-specific graph
bounds in
[`eval_graph_bound`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L582-L642).

Once it has ranges, `GraphEdgeScan` opens KV key cursors over each graph range,
decodes graph keys, and accumulates record ids in
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L260-L393).
For `TargetVertex` mode, new-format pointer keys can carry the target vertex
directly, so the operator can push the target id without reading the edge record;
legacy-format keys fall back to an inner adjacency scan after the outer cursor is
closed in
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L402-L504).
For `TargetId` or `FullEdge` mode, the operator accumulates edge ids and, when
full records are needed, calls `resolve_record_batch` before yielding a
`ValueBatch` in
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L376-L393)
and again for the final flush in
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L529-L536).
The older processor path similarly treats graph traversal results as lookup work
that decodes graph keys and may fetch records in
[`Collectable::prepare`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/dbs/processor.rs#L120-L149)
and [`process_lookup`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/dbs/processor.rs#L173-L193).

So the accurate Surreal statement is: SurrealDB has relation records, graph
adjacency KV keys, graph parsing, and a graph edge scan operator. It is not
missing a graph implementation. Its traversal model is source-record driven and
operator-composed: input record ids flow into graph range scans, graph keys are
decoded, ids are accumulated, and full records are resolved when the query asks
for them.

### Why This Workload Stressed The Two Designs Differently

The FHIR dataframe query is not a single graph idiom like
`person:alice->knows->person`. For each patient, it has to traverse patient to
specimens, specimens to groups, specimens/groups/patient to files, hydrate
`DocumentReference` records, classify nested FHIR coding arrays, deduplicate
overlapping file references, and emit one wide row. The Arango query keeps those
adjacency expansions as AQL traversal clauses in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).
The Surreal path can express graph hops, but the live query and probes had to
move through graph syntax, relation table selects, string record-reference
construction, `IN` filters, array `map`/`reduce`, and target hydration in
[`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
and [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

That is why the recommendation is not "SurrealDB has no graph engine." The
source code says the opposite. The recommendation is narrower: for this
project-wide FHIR graph-to-dataframe workload, Arango's traversal executor kept
more of the fan-out inside a traversal-oriented row pipeline, while the Surreal
implementation pushed the expensive patient-to-file expansion toward
materialization in
[`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go).

### Payload Hydration And Client-Visible Streaming

Both databases still have to hydrate payloads. In Arango, the traversed vertex is
available as an AQL variable, and this repo's query classifies file payloads
directly after building the `files` set in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).
In Surreal, `GraphEdgeScan` can emit target ids or full records, but full-record
output requires batch record resolution through `resolve_record_batch` in
[`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L376-L393).
The current Surreal dataframe query then performs its own nested payload
classification over selected `DocumentReference` records in
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

There is also a client-visible streaming difference in this repo. The Arango
client calls `c.db.Query` with a batch size and then repeatedly calls
`cursor.ReadDocument` in
[`QueryRows`](internal/store/arango/client.go). The Surreal client calls
`surrealdb.Query` and then iterates the returned statement result in
[`QueryRows`](internal/store/surreal/client.go). SurrealDB's engine has streaming
operators internally, but this Go client path did not expose dataframe rows to
the exporter until the statement result returned. For an Elasticsearch bulk
NDJSON export, that matters: the Arango path can report progress while consuming
cursor batches; the Surreal path appeared silent on long live dataframe
statements until the query returned or timed out.

## Database Architecture: What "Graph" Means

ArangoDB and SurrealDB both support document-like records and graph-like
relationships, but "graph" is not the same abstraction in both systems. The
table below links each architectural assertion to either official documentation,
upstream source code, or this repository's implementation.

| Concern | ArangoDB | SurrealDB | Practical effect in this FHIR dataframe workload |
| --- | --- | --- | --- |
| Base data model | ArangoDB's graph model is a labeled property graph: every node and edge is a JSON document with properties, and nodes/edges live in collections. Source: [ArangoDB graph model docs](https://docs.arango.ai/arangodb/3.12/graphs/#graph-model). | SurrealDB stores records in tables and supports record links / relations through record ids and relation tables. Sources: [SurrealDB record links docs](https://surrealdb.com/docs/reference/query-language/language-primitives/record-links), [SurrealDB RELATE docs](https://surrealdb.com/docs/reference/query-language/statements/relate). | Both systems can store raw nested FHIR payloads. This repo uses resource collections/tables plus shared logical collections in [`internal/proto/backend.go`](internal/proto/backend.go). |
| Edge identity | ArangoDB edges are documents in edge collections and always have `_from` and `_to` document references. Source: [ArangoDB graph model docs](https://docs.arango.ai/arangodb/3.12/graphs/#graph-model). | SurrealDB relation records have canonical `in` and `out` endpoints. The source enforces that user data cannot override those endpoints in [`Document::store_edges_data`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs#L129-L133). | The loader had to map Arango-style `_from` / `_to` into Surreal `in` / `out`; see [`insertBatchRelationRaw`](internal/store/surreal/client.go). Without that, `fhir_edge` became non-relation records and traversal failed. |
| Edge adjacency storage | Arango's public model exposes traversable edge collections with `_from` / `_to` and directions `OUTBOUND`, `INBOUND`, and `ANY`. Source: [ArangoDB graph docs](https://docs.arango.ai/arangodb/3.12/graphs/#graph-model). | SurrealDB `RELATE` writes four graph adjacency KV keys per relation: pointer keys from vertices and inner keys from the edge record. Source: [`key/graph/mod.rs`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/key/graph/mod.rs#L1-L36) and [`Document::store_edges_data`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs#L71-L128). | Surreal is not missing an adjacency index. The observed slowdown came after adjacency lookup, when broad fan-out had to compose many graph scans, id sets, target hydration, and nested payload classification. |
| Query language center | AQL has a native graph traversal construct that emits node, edge, and path variables: `FOR node, edge, path IN min..max OUTBOUND|INBOUND|ANY ...`. Source: [ArangoDB traversal docs](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/). | SurrealQL parses graph syntax such as `->`, `<-`, and `<->` into graph lookup parts. Source: [`parse_remaining_idiom`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/syn/parser/idiom.rs#L106-L136). | Arango's query text can keep patient/specimen/group/file expansion as traversal clauses. SurrealQL can express graph hops, but the project-wide dataframe still required explicit intermediate sets and later hydration. |
| Traversal executor | ArangoDB compiles traversal syntax into a dedicated traversal executor. Source: [`TraversalExecutorInfos::parseTraversalEnumeratorSingleServer`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L261-L300), [`TraversalExecutor::doOutput`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L385-L429). | SurrealDB uses `GraphEdgeScan` as a graph edge scanning operator. Its source describes a nested-loop pattern over source record ids and graph edge scans. Source: [`GraphEdgeScan`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L60-L107). | Both have graph execution code. The difference is shape: Arango's executor emits traversal variables into AQL rows; Surreal's graph scan consumes source ids, scans graph key ranges, accumulates ids, and resolves batches. |
| Traversal controls | AQL traversal exposes depth, direction, `PRUNE`, `OPTIONS`, BFS/DFS/weighted order, uniqueness, parallelism, projections, and index hints. Source: [ArangoDB traversal options docs](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/#traversal-options). | SurrealQL exposes graph relation syntax plus general query controls such as `SELECT`, `WHERE`, grouping, statement timeout, and `EXPLAIN`; graph scan behavior is represented by `GraphEdgeScan` rather than a traversal clause with AQL-style depth/options. Sources: [SurrealDB SELECT docs](https://surrealdb.com/docs/reference/query-language/statements/select), [SurrealDB EXPLAIN docs](https://surrealdb.com/docs/reference/query-language/clauses/explain), [`GraphEdgeScan`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L141-L193). | Arango exposes traversal policy directly where the dataframe query needs it. Surreal exposes graph lookup as one operator inside a broader record/query language. |
| Record hydration during traversal | Arango traversal output directly materializes the requested vertex/edge/path registers in [`TraversalExecutor::doOutput`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp#L385-L429). | Surreal `GraphEdgeScan` resolves accumulated record ids with `resolve_record_batch` when full records are required. Sources: [`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L376-L393), final flush at [`GraphEdgeScan::execute`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L529-L536). | In this workload, hydration is followed by heavy nested FHIR classification, so the repeated id-to-record transition in the Surreal query path became visible. |
| Client-visible result delivery in this repo | The Arango backend calls `c.db.Query` with `BatchSize`, then repeatedly calls `cursor.ReadDocument` and invokes the row visitor. Source: [`internal/store/arango/client.go`](internal/store/arango/client.go). | The Surreal backend calls `surrealdb.Query` and then iterates returned statement results. Source: [`internal/store/surreal/client.go`](internal/store/surreal/client.go). | Arango gave useful time-to-first-row progress for export. The Surreal CLI appeared silent for long dataframe statements until the statement returned or timed out, even though Surreal has streaming operators internally. |

The official graph APIs show the same architecture from the user-facing side.
ArangoDB documents traversal as a native AQL construct that returns node, edge,
and path variables:

```aql
FOR node, edge, path
  IN min..max
  OUTBOUND|INBOUND|ANY startNode
  edgeCollection
  PRUNE ...
  OPTIONS ...
```

The [same traversal documentation](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/)
also documents traversal options for BFS/DFS/weighted order, vertex/edge
uniqueness, pruning, projections, parallelism, and traversal index hints.

SurrealDB documents graph edges through
[`RELATE`](https://surrealdb.com/docs/reference/query-language/statements/relate)
and supports graph path syntax such as:

```surql
<-edge_table<-TargetTable
```

The distinction is not capability vs no capability. It is execution shape.
Arango's traversal clause is the center of the query plan for connected-data
expansion. Surreal's graph relation syntax maps to graph lookup/scan machinery
inside a broader record-query language. In this workload, that distinction became
visible once the query had to expand patient -> specimen -> group -> file and
then classify nested FHIR file payloads.

## What Was Implemented

The repository did not give SurrealDB a toy implementation. It contains a real
backend path with native Surreal constructs, implemented behind the same backend
interface opened by [`openBackend`](internal/proto/backend.go).

ArangoDB path:

- creates normal collections and edge collections in
  [`internal/store/arango/client.go`](internal/store/arango/client.go);
- writes batches through Arango's HTTP import API with JSONL in
  [`internal/store/arango/client.go`](internal/store/arango/client.go);
- streams query rows through an Arango cursor in
  [`QueryRows`](internal/store/arango/client.go);
- runs the case/assay query in
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).

SurrealDB path:

- creates normal tables and `TYPE RELATION` tables in
  [`internal/store/surreal/client.go`](internal/store/surreal/client.go);
- converts `_from` / `_to` into Surreal native `in` / `out` record IDs in
  [`insertBatchRelationRaw`](internal/store/surreal/client.go);
- writes edges with `INSERT RELATION INTO fhir_edge` in
  [`internal/store/surreal/client.go`](internal/store/surreal/client.go);
- adds Surreal-specific edge indexes on `project`, `label`, `_from`, and `_to`
  in [`internal/store/surreal/client.go`](internal/store/surreal/client.go);
- uses separate SurrealQL files under `queries_surreal/`;
- includes probe queries to isolate query-stage latency;
- adds a Surreal-only prepare step in
  [`internal/proto/prepare_case_assay.go`](internal/proto/prepare_case_assay.go).

The Surreal implementation had to fix several concrete issues before it was
even a fair ingest target:

- current SurrealDB rejected the old `file://` storage scheme in local runs, so
  the Docker service moved to `surrealkv`; the current service command is in
  [`docker-compose.yml`](docker-compose.yml);
- namespace/database setup had to explicitly define and `USE` the namespace
  before defining and using the database, implemented in
  [`internal/store/surreal/client.go`](internal/store/surreal/client.go);
- `fhir_edge` could not use the generic document path because Surreal relation
  tables require relation records, so `fhir_edge` is special-cased in
  [`InsertBatchRaw`](internal/store/surreal/client.go);
- one shared WebSocket connection made writer concurrency misleading, so Surreal
  writers now open dedicated backend clients in the writer goroutine in
  [`Load`](internal/proto/load.go);
- deletion/truncation of a large relation table could dominate reset time in
  local runs; the truncation behavior itself is implemented through backend
  `Bootstrap` with `Truncate` in [`Load`](internal/proto/load.go) and the Surreal
  table delete path in [`internal/store/surreal/client.go`](internal/store/surreal/client.go).

Those are implementation and operational costs, but they are not the core
database conclusion. The core conclusion comes from the dataframe query, where
the live Arango AQL path and the prepared Surreal path are visible in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql),
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql),
and [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go).

## Load Results: Both Databases Can Ingest the Graph

The attached Arango benchmark loaded the reduced dataset with:

```json
{
  "vertices_inserted": 375954,
  "edges_inserted": 1535906,
  "seconds": 38.608
}
```

A prior Surreal run loaded the same logical count:

```json
{
  "vertices_inserted": 375954,
  "edges_inserted": 1535906,
  "seconds": 156.193
}
```

The load comparison is useful, but it is not the deciding evidence. Loading is
mostly a pipeline problem in this repo's implementation:

- discover and read NDJSON files in [`Load`](internal/proto/load.go),
- validate/build rows through the selected `RowBuilder` in [`Load`](internal/proto/load.go),
- generate edge documents through the row-builder path used by [`Load`](internal/proto/load.go),
- profile fields with the load-time field catalog profiler in [`Load`](internal/proto/load.go),
- batch marshal vertices and edges in the worker pipeline in [`Load`](internal/proto/load.go),
- insert batches through the selected backend in [`insertRawDocuments`](internal/proto/backend.go).

That work is parallel and append-like. Once both backends use native bulk-ish
write paths, both can get data into the database, as shown by matching
`vertices_inserted` and `edges_inserted` in the reduced-dataset summaries above.

The dataframe query stresses a different part of the system. It is not append
I/O. It is repeated graph expansion and nested JSON aggregation, visible in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
and [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).
That is where the databases diverged.

One benchmark caveat: the attached Arango `benchmark` run did not produce a
valid dataframe result because the old benchmark harness passed Surreal/probe
bind variables such as `patient_key` into the Arango AQL query. Arango correctly
rejected the undeclared bind parameter. That harness bug is fixed in
[`internal/proto/query.go`](internal/proto/query.go), and benchmark summaries now
include `dataframe_error` via [`internal/proto/benchmark.go`](internal/proto/benchmark.go).

## Query Results: Where the Comparison Changed

### ArangoDB Live Query

The Arango dataframe query stays close to the data model. It starts with
`Patient`, performs native inbound traversals through `fhir_edge`, and then
aggregates the reached documents in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).

Example:

```aql
LET patient_neighbors = (
  FOR neighbor, edge IN 1..1 INBOUND p fhir_edge
    FILTER edge.project == @project
    FILTER edge.label == "subject_Patient"
    RETURN neighbor
)
```

Then it reuses those neighbors for direct patient-linked resources, and performs
additional graph traversals for specimens, groups, and files in the same AQL
file:

```aql
FOR group, edge IN 1..1 INBOUND specimen fhir_edge
```

```aql
FOR doc, edge IN 1..1 INBOUND group fhir_edge
```

This query is still nontrivial. Earlier measurements showed the query had a
large startup cost. Optimizing the AQL reduced the observed dataframe export
from about `54.4s` to about `31.9s` for `50,270` rows, with first progress
dropping from about `49.2s` to about `26.3s`. These timings are
conversation-captured benchmark outputs, not fresh results from this document
edit; the current harness for rerunning them is [`Benchmark`](internal/proto/benchmark.go).

The important point is not that `31.9s` is perfect. The important point is that
the query stayed live: no extra materialized dataframe table was required to make
the graph traversal plausible. The AQL query reads source collections directly
and does not read `patient_file_rollup` in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).

### SurrealDB Live Query

The Surreal query could express graph navigation:

```surql
LET $specimens = <-(fhir_edge WHERE project = $project AND label = "subject_Patient")<-Specimen;
```

This worked for narrow probes. The failure happened as fanout increased and the
query had to continue from patient to specimen to group to file; the probe and
full query paths are in
[`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
and [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

The live Surreal rollup had to do the following per patient, visible in the
probe query and later moved into Go in [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go):

1. traverse from `Patient` to `Specimen`;
2. convert specimens to string references such as `Specimen/<key>`;
3. query `fhir_edge` for groups whose `_to` is in that specimen ref set;
4. query `fhir_edge` for files whose `_to` is in the specimen ref set;
5. query `fhir_edge` again for files whose `_to` is in the group ref set;
6. union direct, grouped, and patient-level file refs;
7. hydrate `DocumentReference` rows;
8. map/reduce nested FHIR coding arrays to classify assays.

That shape appears in
[`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
and in the full Surreal query
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

The probes showed the problem:

- patient driver scan was fast enough;
- one-hop patient expansion was acceptable;
- patient file rollup was the bottleneck;
- a single nontrivial patient with `17` specimens, `27` groups, and `46` files
  took about `11.6s`;
- broad dataframe execution timed out around `20s` to `30s` before preparation.

These probe timings are conversation-captured command output. The code paths
that produced them are the probe query
[`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
and the shared query runner [`Query`](internal/proto/query.go).

That is the key failure. The graph existed. The references were correct. The
query could produce the right shape for a single patient. The live query was not
viable as a project-wide dataframe generator.

This is where the database design difference became measurable. The expensive
part was not edge existence or syntax. It was the repeated transition:

```text
relation traversal -> intermediate ID array -> relation table SELECT ->
dedup/group -> target record hydration -> nested payload classification
```

ArangoDB also has to hydrate vertex payloads, but its query kept the adjacency
walk and vertex access inside the traversal-oriented AQL flow. SurrealDB's live
query had to assemble and re-use intermediate sets explicitly in SurrealQL; the
current Surreal query shows the prepared-rollup version in
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

## What Went Wrong Exactly

The failure was not that SurrealDB could not store edges. It stored them.

The failure was not that SurrealQL could not traverse an edge. It could, as
shown by the graph syntax in
[`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
and by SurrealDB's upstream `GraphEdgeScan` implementation.

The failure was that the full dataframe query was not one simple edge traversal.
It was a repeated high-fanout expansion where each stage produced arrays that
became inputs to later table queries, followed by nested JSON classification.
In AQL, this stayed inside a traversal-oriented query model. In SurrealQL, it
became a series of graph scans, relation-table selects, array transformations,
`IN` filters, record hydration, and map/reduce work inside one statement, as
visible in [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql)
and [`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql).

The Surreal query became difficult because the natural unit of work for the
database was "records and relations," while the natural unit of work for this
dataframe was "expand all adjacent evidence for each patient and stream a row."
Those are close enough for small graph questions, but they diverge under
project-wide fan-out; the divergence is why [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go)
materializes patient-to-file rollups before the final Surreal dataframe query.

The repo eventually moved the most expensive Surreal part into Go:

```go
patientSpecimens := queryEdgeMap(...)
patientFileKeys := queryEdgeMap(...)
specimenGroups := queryEdgeMap(...)
specimenFiles := queryEdgeMap(...)
groupFiles := queryEdgeMap(...)
```

Then it writes one `patient_file_rollup` document per patient. The final Surreal
query reads that helper table instead of deriving the complete patient-to-file
graph live, as shown by the `patient_file_rollup` read in
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

That is a valid engineering strategy. It is not the same database workload.

The fair comparison therefore has two modes:

| Mode | ArangoDB | SurrealDB |
| --- | --- | --- |
| Live dataframe query | supported by AQL traversal | timed out or became impractical for project-wide rollup |
| Prepared dataframe query | optional | required for a plausible path |
| Benchmark accounting | load + query | load + prepare + query |

If the product requires an interactive dataframe builder where users can compose
new graph paths, ArangoDB has the better execution model. If the product accepts
precomputed rollups per recipe, SurrealDB can still be evaluated, but the
benchmark must include prepare time and must not be described as a live graph
query benchmark. The benchmark harness already treats Surreal prepare time as a
separate phase in [`Benchmark`](internal/proto/benchmark.go).

## Query Language Design Difference

### AQL

AQL is built to mix document access and graph traversal in one language. The
traversal operator emits vertices, edges, and paths directly. It has syntax for
depth, direction, pruning, traversal order, uniqueness, parallelism, and index
hints, as documented in the
[ArangoDB traversal docs](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/)
and implemented by upstream
[`TraversalExecutor`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp).
In this workload, the query language gave the database a compact description of
the graph work:

```aql
FOR neighbor, edge IN 1..1 INBOUND p fhir_edge
```

The rest of the dataframe logic is still regular document processing, but the
adjacency expansion remains visible as graph work in
[`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql).

### SurrealQL

SurrealQL is broader. It combines SQL-like selection, document fields, graph
relations, functions, arrays, record IDs, and embedded scripting-like
expressions, as reflected by the [SurrealDB SELECT docs](https://surrealdb.com/docs/reference/query-language/statements/select),
[RELATE docs](https://surrealdb.com/docs/reference/query-language/statements/relate),
and graph parsing code in
[`parse_remaining_idiom`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/syn/parser/idiom.rs#L106-L136).
That breadth is useful, but in this repo it made the case/assay query harder to
express and optimize as one operation, as shown by
[`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql).

For this workload, the Surreal query frequently had to move between:

- relation traversal syntax,
- relation table `SELECT`s,
- string record-reference construction,
- array `map` / `reduce`,
- `GROUP BY` for deduplication,
- `IN` filters over computed arrays,
- nested payload classification.

That is the core mechanical difference. SurrealDB gives graph relations as one
feature inside a broad multi-model language, backed by
[`GraphEdgeScan`](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs#L60-L107).
ArangoDB gives graph traversal a dedicated execution form inside AQL, backed by
[`TraversalExecutor`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp).

## Benchmark Status

Current evidence should be read carefully:

| Evidence | Interpretation |
| --- | --- |
| Arango load: `375,954` vertices, `1,535,906` edges, `38.608s` | Attached-run output; rerun with [`Benchmark`](internal/proto/benchmark.go) before publication. |
| Arango integrated dataframe result from attached run | Invalid due to old benchmark bind-var bug fixed in [`Query`](internal/proto/query.go). |
| Arango standalone dataframe: about `31.9s`, `50,270` rows | Conversation-captured after AQL optimization; should be rerun with fixed binary. |
| Surreal load: `375,954` vertices, `1,535,906` edges, `156.193s` | Valid prior reduced-dataset load evidence. |
| Surreal live rollup single patient: about `11.6s` for `46` files | Conversation-captured probe output from [`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql). |
| Surreal broad live dataframe | Conversation-captured timeout before materialization; rerun through [`Query`](internal/proto/query.go) if needed. |
| Surreal prepared dataframe | Implemented in [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go), needs fresh benchmark including prepare time. |

The current benchmark harness now records `dataframe_error`, which is necessary
for scientific comparison. A benchmark that silently collapses all query failure
into "not comparable" is not good enough for database evaluation. The relevant
summary fields are defined in [`BenchmarkSummary`](internal/proto/benchmark.go),
and dataframe errors are captured in [`Benchmark`](internal/proto/benchmark.go).

Also, `stage_seconds` in load summaries are cumulative worker timings, not
wall-clock timings. They explain where concurrent work accumulated, but they
should not be summed and compared to top-level `load_seconds`; both fields are
emitted separately by [`LoadSummary`](internal/proto/load.go) and
[`BenchmarkSummary`](internal/proto/benchmark.go).

## Recommendation

ArangoDB should remain the primary backend for the live FHIR dataframe builder.

The reason is specific:

- the data is edge-heavy in the reduced benchmark counts shown above;
- the queries are project-scoped graph expansions, visible in the project
  filters and `INBOUND ... fhir_edge` traversals in
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql);
- the dataframe builder needs flexible traversal composition, described by the
  observed reference and field catalog surfaces implemented in
  [`backend.go`](internal/proto/backend.go), [`discovery.go`](internal/proto/discovery.go),
  and [`field_catalog.go`](internal/proto/field_catalog.go);
- the result path benefits from cursor streaming, implemented in
  [`internal/store/arango/client.go`](internal/store/arango/client.go);
- AQL exposes graph traversal as a first-class operation in the
  [ArangoDB traversal docs](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/)
  and upstream [`TraversalExecutor`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp);
- the existing AQL query can produce the dataframe without precomputing the
  patient-to-file rollup, because
  [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
  does not read `patient_file_rollup`.

SurrealDB should only remain in the comparison under a different contract:

- benchmark `load + prepare + query`, not just `load + query`, because
  [`Benchmark`](internal/proto/benchmark.go) runs
  [`PrepareGDCCaseAssayMatrix`](internal/proto/prepare_case_assay.go) for the
  Surreal backend;
- call the read path prepared/materialized, not live graph traversal;
- use it if fixed recipe rollups are acceptable, represented by
  `patient_file_rollup` in [`backend.go`](internal/proto/backend.go);
- do not use it as evidence that SurrealDB handles the same interactive
  dataframe-builder workload as ArangoDB.

The concise conclusion:

**Both databases can ingest the FHIR graph. ArangoDB is better suited to querying
it live. SurrealDB requires materialization for the dataframe workload that
matters here.**

## Sources

Official documentation:

- [ArangoDB AQL graph traversals](https://docs.arango.ai/arangodb/3.12/aql/graph-queries/traversals/)
- [ArangoDB AQL query cursor API](https://docs.arango.ai/arangodb/3.12/develop/http-api/queries/aql-queries/)
- [ArangoDB HTTP import API](https://docs.arango.ai/arangodb/3.12/develop/http-api/import/)
- [SurrealDB RELATE](https://surrealdb.com/docs/reference/query-language/statements/relate)
- [SurrealDB SELECT](https://surrealdb.com/docs/reference/query-language/statements/select)
- [SurrealDB INSERT](https://surrealdb.com/docs/reference/query-language/statements/insert)
- [SurrealDB EXPLAIN](https://surrealdb.com/docs/reference/query-language/clauses/explain)

Upstream database source:

- [ArangoDB `TraversalExecutor.cpp`](https://github.com/arangodb/arangodb/blob/devel/arangod/Aql/Executor/TraversalExecutor.cpp)
- [SurrealDB graph key layout](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/key/graph/mod.rs)
- [SurrealDB graph edge scan operator](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/exec/operators/scan/graph.rs)
- [SurrealDB graph idiom parser](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/syn/parser/idiom.rs)
- [SurrealDB relation edge persistence](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/doc/edges.rs)
- [SurrealDB graph lookup processor path](https://github.com/surrealdb/surrealdb/blob/main/surrealdb/core/src/dbs/processor.rs)

Repository evidence:

- [`internal/proto/backend.go`](internal/proto/backend.go)
- [`internal/proto/load.go`](internal/proto/load.go)
- [`internal/proto/query.go`](internal/proto/query.go)
- [`internal/proto/benchmark.go`](internal/proto/benchmark.go)
- [`internal/proto/prepare_case_assay.go`](internal/proto/prepare_case_assay.go)
- [`internal/store/arango/client.go`](internal/store/arango/client.go)
- [`internal/store/surreal/client.go`](internal/store/surreal/client.go)
- [`queries/gdc_case_assay_matrix_arango_rows.aql`](queries/gdc_case_assay_matrix_arango_rows.aql)
- [`queries_surreal/gdc_case_assay_matrix_surreal_rows.surql`](queries_surreal/gdc_case_assay_matrix_surreal_rows.surql)
- [`queries_surreal/probes/patient_file_rollup.surql`](queries_surreal/probes/patient_file_rollup.surql)
