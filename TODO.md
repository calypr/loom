# TODO

## Work Package: Project-Partitioned Arango Cluster / SmartGraph Migration

### Goal

Prepare the prototype for a clustered ArangoDB deployment where nearly all traversals are project-local, using `project` as the locality boundary and likely SmartGraph attribute.

### Why

- Current single-instance tuning work shows the dominant cost is edge-heavy write and index maintenance.
- The expected query pattern is project-first filtering followed by traversals inside that project.
- In a cluster, random sharding would scatter related vertices and edges across DB-Servers and force avoidable cross-shard traversal work.
- `project` is the natural partition key if most traversals stay inside one project.

### Scope

- Design the graph layout for clustered deployment.
- Validate whether `project` is the right graph partition key.
- Define the data model changes needed so vertices and edges shard consistently.
- Produce a migration and benchmark plan before any production-facing cutover.

### Deliverables

- A short design note covering:
  - target Arango deployment mode
  - whether to use SmartGraphs
  - shard keys for vertex and edge collections
  - how `project` is derived and enforced on every document
  - expected handling for cross-project edges, if any
- A collection layout proposal:
  - keep single edge collection vs split by relation/label
  - per-resource vertex collections vs consolidated collections
  - required indexes only
- A migration plan from the current single-instance prototype layout.
- A benchmark plan comparing:
  - single instance vs cluster
  - current layout vs project-partitioned layout
  - traversal latency
  - load throughput
  - index/write amplification

### Open Questions

- Is `project` present and stable on every vertex and every edge at write time?
- Are any traversals expected to span projects in a meaningful percentage of cases?
- Should `project` come from the CLI flag, from FHIR source data, or from a canonical derived mapping?
- Are there hot projects large enough to create shard skew?
- Do we want one logical graph for all projects, or project-isolated graphs/collections?

### Tasks

1. Audit locality assumptions.
   - Measure what fraction of current and expected traversals are truly project-local.
   - Identify any cross-project references in the loaded FHIR graph.

2. Audit the current write model.
   - Confirm every vertex stores `project`.
   - Confirm every edge stores `project`.
   - Confirm `_from` and `_to` always point to vertices in the same project unless explicitly allowed.

3. Define the cluster graph model.
   - Choose SmartGraph vs plain sharded collections.
   - Choose shard keys for each vertex collection.
   - Choose shard keys for edge collections so adjacent data stays colocated as much as possible.

4. Revisit collection layout before clustering.
   - Evaluate splitting `fhir_edge` by relation type or label.
   - Remove any edge secondary indexes that are not needed once traversal starts from project-filtered vertices.

5. Build a synthetic benchmark matrix.
   - Small project, medium project, large project.
   - Mostly local traversals.
   - Worst-case cross-project traversals.
   - Load benchmarks with current and proposed layouts.

6. Prototype the cluster-targeted loader changes.
   - Ensure shard-key fields are populated before insert.
   - Ensure key construction and edge generation are compatible with the chosen shard model.
   - Add guardrails so malformed rows cannot produce missing-partition documents.

7. Document migration and rollback.
   - Fresh-load path into new clustered collections.
   - Validation queries for row counts, edge counts, and traversal parity.
   - Clear rollback path to the single-instance layout.

### Exit Criteria

- We have a written decision on whether `project` should be the SmartGraph attribute.
- We have a concrete collection and shard-key design.
- We have benchmark evidence that the clustered layout improves project-scoped traversals without making ingest unacceptable.
- We know how to migrate without guessing mid-cutover.
