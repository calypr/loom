# Architecture postmortem: why Loom and the Explorer Builder do not cooperate

## Executive conclusion

Your suspicion is justified: Loom needs a substantial restructuring around its Explorer authoring boundary, catalog semantics, lifecycle orchestration, and ingestion jobs.

It does **not** need a complete rewrite of its FHIR extraction, graph compiler, Arango access, or ClickHouse publication machinery. Those pieces contain useful, tested work. The problem is that they are currently connected through APIs that expose internal complexity directly to the frontend.

Three architectural mistakes explain most of the behavior:

1. **Loom exposes observed data as if it were executable capability.**  
   A resource, relationship, or field appearing in a catalog does not mean the compiler can use it. The frontend nevertheless treats catalog entries as valid choices.

2. **Editing, validation, compilation, preview, publication, materialization, and activation are mixed together.**  
   The frontend sends a complete mutable bundle repeatedly, while Loom recompiles it through multiple layers on reconciliation, preview, and publication.

3. **Graph ingestion is coupled to expensive, dataset-wide metadata profiling.**  
   The loader streams input rows, but simultaneously builds large in-memory catalogs whose size historically grew with data cardinality. That is why a “streaming” immutable-generation migration reached approximately 4.1 GB RSS.

The frontend errors are therefore not random in origin. They are deterministic failures from several backend layers, discovered too late and translated inconsistently. They appear random to users because the UI is allowed to construct states that Loom cannot execute, and the backend does not return a stable recovery model.

No code was modified during this review.

---

# 1. What Loom currently does

There are really two major systems inside Loom.

## Graph-generation pipeline

```text
repair-generation
  ├─ audit old fhir_edge generation
  ├─ ingest.Load
  │   ├─ discover META files
  │   ├─ load graph schema
  │   ├─ preflight all files
  │   ├─ create LOADING generation manifest
  │   └─ for each file
  │       └─ loadFile
  │           ├─ scanner → buffered line queue
  │           ├─ parser/profile workers
  │           │   ├─ generated or generic row builder
  │           │   ├─ generation-key rewrite
  │           │   ├─ field profiling
  │           │   ├─ vertex batch
  │           │   └─ edge batch + relationship counts
  │           └─ buffered write queue → Arango writers
  ├─ write field catalogs
  ├─ write relationship catalog
  ├─ mark generation STAGED
  ├─ audit new generation
  └─ optionally activate generation
```

The central orchestration is in [generation_orchestration.go](../internal/ingest/generation_orchestration.go), while the concurrent file pipeline is in [generation_workers.go](../internal/ingest/generation_workers.go).

## Explorer authoring pipeline

```text
browser-only draft
  │
  ├─ debounce
  └─ POST complete bundle to /builder
        ├─ validate authoring document
        ├─ validate catalog snapshot
        ├─ normalize route occurrences
        ├─ validate catalog nodes/edges/candidates
        ├─ lower authoring document to recipe
        ├─ validate recipe
        ├─ resolve schema
        ├─ build semantic plan
        ├─ lower physical plan
        ├─ optimize plan
        └─ return reconstructed builder state

Preview:
  complete bundle → resolve/compile again → AQL → Arango

Publish:
  complete bundle → resolve/compile again
                  → materialize outputs
                  → ClickHouse publication
                  → dataset release activation
                  → Explorer revision persistence
```

The main authoring compiler is [explorer_authoring_v1.go](../internal/server/explorer_authoring_v1.go), with HTTP lifecycle handling in [explorer_authoring_v1_routes.go](../internal/server/explorer_authoring_v1_routes.go).

The critical observation is that `/builder` is not a lightweight editing API. It is effectively a full compiler endpoint.

---

# 2. Why the screenshot happens

The `qualification` failure is a perfect example of the larger architectural defect.

The historical edge extractor permitted nested/backbone definitions to leak into persisted edge endpoint metadata. It therefore produced relationships resembling:

```text
Practitioner → qualification
```

The Builder catalog intentionally reverses stored reference direction so that a user can start from the referenced resource and traverse toward resources that refer to it:

```aql
COLLECT
  from_type = d.to_type,
  label = d.label,
  to_type = d.from_type
```

That happens in [store.go](../internal/catalog/arango/store.go).

Consequently, a malformed stored edge becomes:

```text
qualification → Practitioner
```

in the Builder graph.

The frontend then:

- renders every catalog node as a resource;
- labels catalog nodes as “Available row start”;
- permits any inspected node to become the row start;
- has no `rootEligible`, `executable`, or `blockedReason` property.

That behavior is visible in `GuidedGraphWorkspace.tsx` and `RouteExtensionPanel.tsx` in the IDP-Frontend repository.

Only after the frontend submits the document does Loom reach the compiler’s row-grain check and return `UNSUPPORTED_ROW_GRAIN`.

This is not a Practitioner-specific or qualification-specific bug. Any backbone element, nested definition, arbitrary schema definition, or invalid endpoint can produce the same failure.

The general invariant must be enforced at four boundaries:

- extraction: only concrete FHIR resources may become endpoints;
- persistence/audit: reject non-concrete endpoint metadata;
- catalog construction: never advertise invalid resources;
- authoring capability generation: advertise only operations the compiler has already validated.

The current dirty Loom tree contains work toward a general `ConcreteResourceType` boundary and catalog filtering. That is the correct direction, but it does not solve the broader observation-versus-capability problem.

---

# 3. The catalog is not a capability contract

This is the deepest frontend/backend mismatch.

## What field profiling actually does

For every resource payload, the profiler recursively observes:

- paths that occur;
- broad kinds such as scalar, object, or array;
- document counts;
- distinct scalar values;
- CodeableConcept-derived pivot columns;
- FHIR extension URLs and value forms;
- shape fingerprints used to reuse traversal plans.

The implementation is in [write_profiler.go](../internal/catalog/write_profiler.go).

The resulting `fhir_field_catalog` is useful for:

- showing fields that are populated in this particular dataset;
- supplying filter value suggestions;
- discovering dynamic FHIR extensions;
- proposing pivot columns;
- avoiding presenting every theoretical FHIR field;
- enriching recipe schema resolution.

But it does **not** prove that a field can be compiled.

The profiler walks observed JSON. The compiler separately applies generated FHIR schema rules such as:

- whether the path exists in the supported generated schema;
- whether each array segment has the required `[]`;
- whether the terminal value is scalar;
- whether the projection is valid at the selected row grain;
- whether the path can be lowered to AQL;
- whether a pivot or filter operation is supported.

Those checks occur much later in files such as [selector_validation.go](../internal/dataframe/spec/selector_validation.go).

Therefore:

```text
observed in JSON ≠ valid selector
catalog relationship ≠ legal traversal
catalog node ≠ valid row root
```

The frontend currently assumes all three equivalences.

## Where profiling belongs

Field profiling should not be on the critical path for graph correctness or generation activation.

A better ownership model is:

- generated FHIR schema defines legal paths, cardinality, row grains, and operations;
- graph ingestion writes resources and valid relationships;
- an asynchronous enrichment job computes populatedness, approximate counts, extensions, distinct-value suggestions, and pivots;
- a capability compiler intersects schema capability with observed enrichment.

Separating profiling from graph loading would not lose the graph or its relationships. Loom would temporarily lose only dataset-specific conveniences:

- knowledge that a legal field is actually populated;
- immediate filter-value suggestions;
- dynamic extension discovery;
- observed pivot values;
- field frequency rankings.

Those can appear asynchronously or be computed lazily. The UI could initially show valid schema fields and progressively enrich them.

---

# 4. The frontend can construct many invalid states

The issue is much broader than `UNSUPPORTED_ROW_GRAIN`.

## Failure families exposed to the frontend

| Failure family | Representative backend codes |
|---|---|
| Protocol and document shape | `MALFORMED_AUTHORING_REQUEST`, `UNSUPPORTED_AUTHORING_PROTOCOL`, `UNSUPPORTED_DOCUMENT_VERSION`, `MISSING_AUTHORING_IDENTITY` |
| Identifier integrity | `MISSING_OUTPUT_ID`, `DUPLICATE_OUTPUT_ID`, `DUPLICATE_AUTHORING_DOCUMENTS`, `DUPLICATE_ROUTE_EDGE`, `DUPLICATE_ROUTE_OCCURRENCE`, `DUPLICATE_CANDIDATE_ID`, `DUPLICATE_EMISSION_ID` |
| Catalog lifecycle | `SNAPSHOT_REQUIRED`, `CATALOG_UNAVAILABLE`, `CATALOG_INCOMPLETE`, `CATALOG_SNAPSHOT_FAILED`, `STALE_CATALOG_SNAPSHOT` |
| Route topology | `STALE_ROUTE_NODE`, `STALE_ROUTE_EDGE`, `INVALID_ROUTE_CONTINUITY`, `INVALID_ROUTE_ORIGIN`, `INVALID_ROUTE_OCCURRENCE_ORDER`, `INVALID_ROUTE_TERMINAL_NODE`, `ROUTE_OCCURRENCES_REQUIRED` |
| Selection/candidate integrity | `STALE_CANDIDATE_ID`, `CANDIDATE_REFERENCE_MISSING`, `SELECTION_NODE_MISMATCH`, `CANDIDATE_OCCURRENCE_REQUIRED`, `DUPLICATE_CANDIDATE_OCCURRENCE` |
| Compiler capability | `UNSUPPORTED_ROW_GRAIN`, `UNSUPPORTED_NESTED_TRAVERSAL`, `INVALID_AUTHORING_INTENT`, `INVALID_COMPILED_RECIPE`, `AUTHORING_OUTPUT_LOWERING_FAILED` |
| Presentation capability | `UNSUPPORTED_FILTER_PRESENTATION`, `UNSUPPORTED_CHART_PRESENTATION`, `STALE_EMISSION` |
| Preview | `INVALID_PREVIEW_LIMIT`, `UNKNOWN_AUTHORING_OUTPUT`, `PREVIEW_UNAVAILABLE`, `AUTHORING_PREVIEW_FAILED` |
| Publication | `PUBLICATION_UNAVAILABLE`, `SNAPSHOT_CONFLICT`, `MATERIALIZATION_FAILED`, `MATERIALIZATION_ACTIVATION_FAILED`, `REVISION_STORE_FAILED` |
| Identity and authorization | `AUTHORING_IDENTITY_CONFLICT`, `FORBIDDEN`, `NOT_FOUND` |
| Infrastructure fallback | `AUTHORING_COMPILER_UNAVAILABLE`, `ACTIVE_AUTHORING_BUNDLE_INVALID`, `INTERNAL_ERROR` |

Many of these are appropriate backend checks. The problem is that the contract does not tell the frontend:

- which choices are legal before the user selects them;
- which error is attached to which editable object;
- whether the error is recoverable;
- what recovery action should be taken;
- whether an error invalidates the draft, catalog snapshot, compilation, or publication.

## The frontend discards most diagnostic information

The API error type can carry status, code, diagnostics, request ID, details, and retryability. But the workspace normally renders only the first diagnostic’s message and code.

It discards or hides:

- all subsequent diagnostics;
- diagnostic stage;
- JSON path;
- structured details;
- request ID;
- recovery action;
- whether retrying is safe;
- which table, route occurrence, or candidate failed.

For the initial Builder load, it can reduce the result even further to a generic “could not be loaded” state.

## Backend errors are not consistently typed

Loom has a typed dataframe error package, but the authoring boundary does not consistently preserve it. Generic compiler failures are converted using:

```go
message := err.Error()
code := "INVALID_AUTHORING_INTENT"
if strings.Contains(strings.ToLower(message), "traversal") {
    code = "UNSUPPORTED_NESTED_TRAVERSAL"
}
```

That creates three problems:

- many distinct failures collapse into `INVALID_AUTHORING_INTENT`;
- classification depends on English error text;
- raw internal error wording reaches the user.

This directly explains the “random backend error” experience.

---

# 5. Frontend state ownership is unstable

The current hard-cutover frontend is considerably smaller than its predecessor, but several structural problems remain.

## Browser-only draft

The editable draft lives primarily in the React reducer. There is no normal server-side saved draft with compare-and-swap semantics.

Consequences:

- refresh can lose work;
- another browser cannot continue the draft safely;
- active revision and local draft can diverge;
- backend reconciliation is being used partly as validation and partly as normalization.

## Whole-bundle reconciliation

Every semantic edit triggers a debounced POST containing the complete authoring bundle.

The frontend ignores stale responses by generation number, but it does not cancel obsolete HTTP requests. Loom still performs every obsolete compilation.

## Frontend and backend sometimes resolve different documents

`completeDocument` omits incomplete tables from the outgoing bundle.

Thus the UI may display an incomplete table while the backend successfully resolves a bundle that does not contain that table. The word “reconciled” does not mean the visible workspace was completely validated.

## Stale-snapshot recovery is unsafe

On a stale snapshot, the frontend refetches Builder state and then retries the old bundle against the new snapshot.

That can fail because:

- catalog IDs are opaque and may have changed;
- the refetch can hydrate active state over local state;
- no semantic mapping exists from old catalog identities to new identities;
- there is no draft revision or merge protocol.

## Runtime types have already drifted

A documented failure involved Loom returning `candidateEmissions: null` while the frontend declared it as an array and crashed. Current code guards against that specific case, and the backend now serializes `[]`.

That incident is evidence of the actual problem: Go and TypeScript separately describe the contract, and no generated or cross-repository conformance boundary guarantees they agree.

---

# 6. Is repair-generation actually streaming?

No. It is a streaming input pipeline attached to several accumulating data structures.

The source file is not loaded wholesale, but “does not read the entire file into one slice” is much weaker than constant-memory streaming.

## Structures retained beyond one row or write batch

- up to 10,000 buffered input lines in the committed implementation;
- eight parser workers;
- one vertex batch and one edge batch per parser worker;
- up to 100 queued write batches;
- import-request body copies;
- pooled byte buffers retaining their high-water capacity;
- one field profiler per worker for the entire file;
- a shared shape-plan cache for the entire file;
- merged field profiler state;
- merged field profilers retained for every resource type until the full load completes;
- one relationship-count map per writer;
- file-level and generation-level relationship aggregations;
- Arango’s indexes, import transaction state, caches, and compaction memory;
- both old and new generation data and index entries in the same collections.

Preflight also reads the inputs before ingestion, so the 4.5 GB source is traversed at least twice, although preflight does not retain all rows.

## Memory budget

The Go estimates below illustrate the committed pipeline at 500-row batches, eight parser workers, a 10,000-line input queue, and a 100-task write queue. They are formulas and plausible ranges, not heap-profile measurements. Actual FHIR document sizes vary heavily.

| Component | Approximate budget | Controlling bound | Growth class |
|---|---:|---|---|
| Input line queue | `10,000 × average line size`; roughly 20–60 MB at 2–6 KB/row | Queue length | Bounded by queue |
| Active parser state | Approximately 8 active rows with typed objects, generic maps, edges and temporary buffers; roughly 10–100 MB | Worker count and largest row | Worker-bounded |
| Vertex batches | `8 × 500 × encoded vertex size`; roughly 8–24 MB | Workers × batch size | Batch/worker-bounded |
| Edge batches | `8 × 500 × edge size`, but rows can emit many edges; roughly 1–20+ MB | Workers × batch size and edge fan-out | Batch/worker-bounded |
| Queued write batches | Up to `100 × 500 × encoded document size`; roughly 50–300+ MB | Queue tasks × batch size | Queue/batch-bounded |
| Field distinct values | Historically potentially multiple GB | Number of distinct values across observed paths, duplicated across workers | Dataset-cardinality growth |
| Shape plans | One plan per unique observed structural fingerprint | Unique payload shapes | Dataset-cardinality growth |
| Relationship maps | Usually KB to low MB | Distinct `(project,generation,path,from,label,to)` tuples | Normally schema-bounded |
| JSON copies | Often 50–500+ MB overlapping other rows/batches | Workers, queues, batch sizes and largest documents | Worker/batch-bounded |
| Import request copy | One body copy per active writer; pooled buffers may retain capacity | Writers × batch bytes | Writer/batch-bounded |
| Arango import state | One or more request/transaction batches | Concurrent writers and batch bytes | Writer/batch-bounded |
| Arango persistent indexes | Grows with all documents and generations | Data cardinality × index count | Database-cardinality growth |
| Arango cache/compaction | Can reach GB-scale independently of Go | Working set and database configuration | Database/server policy |

The field profiler is the best explanation for the observed 4.1 GB Go RSS.

Each worker retained its own distinct-value maps across the entire file. High-cardinality FHIR fields can include:

- IDs;
- identifiers;
- references;
- URLs;
- attachment locations;
- hashes;
- timestamps;
- extension values;
- coding combinations.

After a file completes, Loom creates a merged profiler while worker-local profilers may still exist. It then retains the merged profiler in the generation-wide `catalogs` map until every file is complete.

The current dirty tree adds finite limits and smaller queues. That is an improvement over unbounded retention, but the defaults are not a credible aggregate memory budget:

```text
4,096 fields
× 4,096 values per field
× up to 4,096 bytes per value
≈ 64 GiB of raw value bytes per profiler
```

That excludes maps, strings, slices, extension observations, pivots, and multiple worker profilers. “Finite” is not synonymous with operationally safe.

Memory limits should be established by the Kubernetes workload and Go runtime environment, not by adding server-owned memory policy.

---

# 7. Why one writer and 500-row batches still OOMed

Those controls reduced only part of the memory equation.

Reducing writers controls:

- concurrent HTTP import bodies;
- writer-local relationship maps;
- simultaneous Arango requests.

Reducing batch size controls:

- vertex batch size;
- edge batch size;
- each queued task’s payload;
- each import body.

Neither controls:

- field distinct-value accumulation;
- shape-plan accumulation;
- worker-local catalog duplication;
- merged catalogs retained across files;
- the eight parser workers in the committed pipeline;
- the 10,000-line queue;
- the number of queued tasks;
- Arango index growth;
- old and new generations coexisting physically.

A slower writer can also keep the write queue full for longer. Smaller batches mean each queue slot is smaller, but 100 queued batches plus parser-owned batches remain live.

So the batch change shaved hundreds of MB at best while the dominant structure continued growing toward dataset cardinality.

---

# 8. JSON amplification

One input line may exist in several forms during processing:

1. scanner-owned bytes;
2. queued Go string;
3. `[]byte(line.text)`;
4. generated typed FHIR representation or generic `map[string]any`;
5. vertex envelope;
6. serialized vertex bytes;
7. serialized edge bytes;
8. decoded `map[string]json.RawMessage` for generation namespacing;
9. re-serialized generation-qualified vertex and edges;
10. profiling payload map;
11. concatenated import body;
12. copied import body for HTTP transmission.

The generation wrapper alone decodes and re-encodes every vertex and edge in [generation_identity.go](../internal/ingest/generation_identity.go). The import path builds a pooled buffer and then makes another copy in [client.go](../internal/store/arango/client.go).

These copies are mostly bounded by concurrency and buffering, but they amplify every other memory-retention decision.

---

# 9. Generation model evaluation

| Model | Strengths | Costs and risks | Assessment |
|---|---|---|---|
| Shared collections with generation-qualified keys | Stable collection names; manifest pointer can switch atomically; existing queries can add a generation predicate | Every generation shares every index; all queries must scope correctly; cleanup requires large filtered deletes; collection/index working set grows; old and new coexist during repair | Workable with strict retention and ample Arango capacity, but operationally expensive |
| Separate collections per generation | Strong isolation; old generation cannot be mutated accidentally; activation changes a collection mapping; dropping a generation is cheap and complete | Dynamic collection resolution; many collections and indexes; bootstrap overhead; compiler must never hard-code names | Better immutable isolation, but needs a deliberate collection resolver and retention policy |
| Database-side copy/rewrite | Avoids rereading META; can stream/copy authoritative vertices; much lower Go parse/profile cost | Still writes a second set of documents and indexes; must batch carefully; edge/key rewriting must be exact | Best repair mechanism when stored vertices are trusted |
| Full source re-ingestion | Reproducible from canonical source; exercises current parser, validation and extraction; appropriate after broad transformation changes | Worst I/O, CPU and memory; repeats profiling; duplicates every index entry; can fail hours into the operation | Reserve for cases where resource payloads themselves are suspect |

For long-term clarity, separate physical collections—or a similarly isolated generation namespace—fit immutable generations better. But this should not be changed casually: Loom’s compiler and query layer need one generation-to-storage resolver first.

The Explorer restructuring does not need to wait for that storage migration.

---

# 10. Does edge repair require parsing the entire META directory?

No.

There are three repair scopes.

## Relationship catalog only

If `fhir_edge` is correct but `fhir_relationship_catalog` is contaminated, rebuild the catalog from valid edges. No FHIR source parsing is needed.

The current rebuild query already has the shape of this approach and filters endpoints against concrete resource types.

## Remove malformed edges while preserving the graph

If malformed edges can be identified by invalid endpoint types, a new immutable generation can be constructed by:

- streaming/copying old-generation vertices into new-generation keys;
- copying valid edges while rewriting their generation-qualified endpoints;
- omitting invalid edges;
- rebuilding relationship counts;
- copying or asynchronously regenerating field enrichment;
- auditing and activating the new generation.

This inserts new documents and does not mutate the old generation.

## Regenerate possibly missing or incorrect edges

If the extractor may also have missed valid edges, edge extraction must run again. But it can run from FHIR payloads already stored in resource vertices.

It may be sufficient to re-extract only source resources associated with invalid edges. If the faulty extractor’s scope is unknowable, all stored vertices may need to be inspected—but that is still not the same as rereading and fully re-ingesting the entire META source.

Full META re-ingestion is required only when:

- stored resource payloads are incomplete or wrong;
- the resource transformation itself changed;
- authoritative provenance requires recreation from source;
- schema validation must be rerun against the original files.

---

# 11. The lifecycle design is unfinished

The frontend and Loom planning documents describe a safer model:

- save a draft with compare-and-swap;
- compile to an immutable receipt;
- preview by compilation ID;
- publish by compilation ID;
- protect against external updates;
- expose blocked candidates and diagnostic eligibility.

The actual implementation instead upgraded the existing Builder endpoint:

- `/builder` accepts and recompiles the whole bundle;
- `/preview` accepts and recompiles the whole bundle;
- `/publish` accepts and recompiles the whole bundle;
- the compilation receipt exists largely as an internal return structure;
- draft persistence and CAS are absent;
- publication behaves essentially as last-writer-wins.

This is why small UI actions cross so many backend layers.

Loom also retains a large legacy Explorer V2 lifecycle/compiler implementation in [explorer_v2_lifecycle.go](../internal/server/explorer_v2_lifecycle.go), alongside the new V1 authoring model and the older repository-upload route in [explorer_config_route.go](../internal/server/explorer_config_route.go).

Even where the legacy routes are no longer registered, tests and internal machinery keep much of the model alive. The result is multiple overlapping definitions of:

- Explorer config;
- authoring bundle;
- draft;
- revision;
- recipe;
- compiled config;
- materialization;
- dataset release;
- publication.

That is genuine architectural sprawl, not just a file-organization problem.

---

# 12. Recommended target architecture

The restructuring should be radical at the boundary but conservative in proven internals.

## A. Immutable graph ingestion job

Responsibilities:

- validate and load concrete FHIR resources;
- create valid resource-to-resource edges;
- stage an immutable generation;
- audit structural invariants;
- activate only after validation.

Run it as a dedicated Kubernetes Job, not via `kubectl exec` inside the Loom server pod.

The Docker image can contain both binaries, but the Job should override the server entrypoint and own:

- CPU request/limit;
- memory request/limit;
- `GOMEMLIMIT`;
- `GOMAXPROCS`;
- retry/backoff policy;
- temporary-storage limits.

The Loom server should not decide these policies.

## B. Capability snapshot compiler

Create a generation-scoped capability document from:

```text
generated FHIR schema
∩ valid graph relationships
∩ observed populatedness/enrichment
∩ authorization scope
∩ supported compiler operations
```

Every advertised node and candidate should carry explicit semantics such as:

```json
{
  "resourceType": "Practitioner",
  "rowRootEligible": true,
  "supportedOperations": ["SELECT", "FILTER"],
  "blockedReason": null
}
```

Invalid nodes may be retained for audit visibility but must not appear as selectable resources.

A powerful invariant test then becomes possible:

> Every advertised row root, route edge, and column candidate must compile successfully under the snapshot that advertised it.

### The traversal Builder is the product contract

The Explorer graph in the frontend is intended to be a real authoring surface
over Loom's FHIR graph. Loom must make that vision true. A user should be able
to choose a concrete FHIR resource as the row root, follow relationships Loom
actually stores, select fields from any occurrence along the route, and compile
that construction into an executable Arango query.

The frontend should not need to reproduce the FHIR schema, understand Arango
edge direction, or predict dataframe cardinality rules. Those are Loom
responsibilities.

### Current Loom gap

The current catalog reader assembles Builder nodes, edges, and selections
directly from populated-field and populated-relationship discovery. The public
types carry only:

- node ID and resource type;
- edge ID, source node, target node, and label; and
- candidate ID, node ID, field reference, logical type, and coarse filter/chart
  flags.

This is enough to draw a graph, but it is not enough to promise that the graph
can be compiled. In particular:

- a node can be observed without having a supported row grain;
- a relationship can be observed without a valid generated traversal;
- a field can be observed without being a legal generated selector;
- an object or array can require a projection mode the catalog does not
  describe;
- repeated resource types require occurrence-specific field binding;
- Builder direction is the inverse of stored FHIR-reference direction and must
  be made unambiguous;
- the hard-coded four-hop authoring limit is not represented in the catalog;
  and
- catalog completeness currently means discovery completed, not that every
  returned capability passed the compiler.

### Required meaning

A capability snapshot must be an immutable, generation-scoped,
authorization-scoped, compiler-versioned statement of what Loom promises it can
execute.

The generated FHIR schema and compiler define validity. Observation data only
annotates valid capabilities with populatedness, edge counts, and suggested
values. An observed fact must never create a capability the schema/compiler do
not support.

The snapshot identity must cover:

- canonical project identity;
- immutable dataset generation;
- authorization-scope digest;
- generated FHIR schema digest;
- relationship-catalog digest;
- field-enrichment digest or version;
- authoring protocol version;
- compiler artifact version;
- traversal policy version; and
- the canonical capability payload.

Changing any of these produces a different snapshot token. A snapshot is never
silently modified in place.

### Node contract

Every node offered to the current Builder must represent a concrete FHIR
resource collection with a supported row grain:

~~~json
{
  "nodeId": "n_...",
  "resourceType": "Practitioner",
  "rowRootEligible": true,
  "rowGrain": "RESOURCE",
  "populated": true,
  "documentCount": 8124,
  "blockedReason": null,
  "capabilityVersion": "..."
}
~~~

For compatibility with the current frontend, Loom should initially omit
non-root-eligible nodes from the normal nodes array. The UI currently treats
every returned node as a selectable row start. A future frontend may render
blocked nodes through a separate collection or an eligibility property it
explicitly understands.

Node eligibility must be decided by the same row-grain registry used during
lowering. It must not be inferred from collection existence or profiler rows.

### Traversal-edge contract

An advertised Builder edge must mean:

> From an occurrence of the source node, Loom can follow this exact
> relationship in this authoring direction, under the pinned generation and
> authorization scope, and lower the step into a valid graph traversal whose
> result is an occurrence of the target node.

The internal capability requires more information than the frontend currently
renders:

~~~json
{
  "edgeId": "e_...",
  "fromNodeId": "n_patient",
  "toNodeId": "n_document_reference",
  "label": "subject_Patient",
  "storageDirection": "INBOUND",
  "sourceResourceType": "DocumentReference",
  "targetResourceType": "Patient",
  "observedEdgeCount": 1002431,
  "allowsRepeatedTarget": true,
  "blockedReason": null
}
~~~

The current frontend can continue consuming only edge ID, source, target, and
label. The additional internal fields let Loom define and test the promise.

Every edge must pass all of the following before publication:

- both endpoints canonicalize to concrete generated FHIR resource types;
- both endpoint nodes exist in the same snapshot;
- the label resolves through the generated traversal registry;
- Builder direction maps unambiguously to stored edge direction;
- project, generation, and authorization predicates can be applied;
- the lowerer produces a one-step physical traversal plan; and
- the renderer produces valid AQL for that plan.

Malformed historical relationships may remain visible to audit tooling, but
must never enter the Builder capability graph.

The arbitrary four-hop policy requires an explicit decision. The current
frontend presents a traversal graph without explaining that limit. Loom should
either remove the fixed authoring limit and rely on physical plan cost/safety
policy, or publish a route policy the frontend can enforce. Quietly accepting
four steps and rejecting the fifth is not a solid contract.

### Column-candidate contract

A profiler record is only input evidence. Before a field becomes a Builder
candidate, Loom must resolve it through generated selector and terminal-type
metadata.

Each internal candidate should describe:

- canonical selector;
- owning node;
- terminal FHIR type;
- scalar/object/array cardinality;
- allowed projection modes;
- supported filter operators;
- supported chart aggregations;
- whether the field was observed;
- observed document count;
- bounded value suggestions; and
- any reason it was excluded or restricted.

~~~json
{
  "candidateId": "s_...",
  "nodeId": "n_observation",
  "label": "Observation.code.coding[].code",
  "logicalType": "string",
  "cardinality": "MANY",
  "projectionModes": ["FIRST", "ARRAY", "DISTINCT_ARRAY"],
  "filterOperators": ["EQUALS", "IN", "EXISTS"],
  "filterable": true,
  "chartable": true,
  "populated": true
}
~~~

For the current frontend, Loom should return only candidates safe under the
frontend's existing default projection behavior. Richer candidates can be
exposed when the frontend can choose projection modes explicitly. This ensures
every currently clickable field works while preserving richer future
capabilities.

Candidates remain attached to node types in the snapshot. The authoring
document binds a candidate to a route occurrence. That preserves the necessary
distinction when one route visits the same resource type more than once.

### Deterministic snapshot construction

Loom should build a snapshot in these phases:

1. Resolve the exact active or requested immutable generation.
2. Resolve the caller's authorization scope for that generation.
3. Enumerate concrete resource collections visible in that scope.
4. Intersect them with the compiler's supported row-grain registry.
5. Read observed relationship classes and reject any class absent from the
   generated traversal registry.
6. Normalize Builder direction and create snapshot-local opaque edge IDs.
7. Read observed fields, but validate every path through generated selector and
   terminal-type metadata before creating a candidate.
8. Derive projection, filter, and chart operations from compiler semantics.
9. Canonically sort and serialize nodes, edges, candidates, policies, and
   version identities.
10. Hash and persist the complete immutable snapshot.

If a required dependency fails, Loom returns an explicitly unavailable
snapshot. It must not return a partial graph that looks usable.

### Executable invariants

The central promise should be tested as:

- every node compiles as a zero-hop output root;
- every edge compiles as a one-hop extension from its source node;
- every candidate compiles at an occurrence of its node using each advertised
  projection mode;
- every advertised filter/chart operation passes semantic validation;
- every compiled output renders valid AQL;
- graph direction and endpoint types agree with the rendered AQL;
- opaque IDs resolve only inside the snapshot that created them; and
- incomplete or truncated snapshots never appear as usable Builder state.

Representative integration fixtures should also run Arango EXPLAIN on generated
queries. That catches renderer, index, and direction mistakes pure compiler
tests cannot.

This snapshot is the main mechanism for making the existing traversal UI
trustworthy. Most current Builder errors should become impossible to construct,
not merely better explained after submission.

## C. Server-owned draft

Use an immutable or versioned draft:

```text
PUT draft
If-Match: draftRevision
```

The response produces a new draft revision. Refresh, multiple tabs, and conflicts become explicit.

## D. Immutable compilation receipt

```text
POST .../authoring/v2/compile
  V2 document + exact capability snapshot token

→ receiptId + normalized Builder state + public output columns
```

Compilation is side-effect free with respect to graph data, materialization,
publication, and active pointers. Persisting the immutable receipt is part of
compilation and must finish before Loom returns success.

### Implemented boundary

The production compiler now translates V2 intent directly. It does not route
through the V1 compatibility compiler. It authorizes the exact active snapshot,
translates route occurrences and candidate selections into a native recipe,
compiles the already resolved recipe without catalog discovery, creates a
versioned receipt, and persists it through the immutable receipt store.

Preview and publication accept a receipt ID and execute the receipt's frozen
resolved recipe. They never reinterpret the authoring document, migrate it
through V1, or rediscover fields.

### Semantic guarantee

A receipt must mean:

> Loom accepted this exact authoring intent against this exact capability
> snapshot, resolved every route occurrence and candidate identity, produced
> this exact output schema, and created an executable artifact that can be
> previewed or published without interpreting the authoring document again.

The receipt must pin:

- project and Explorer identity;
- canonical intent digest;
- capability snapshot token;
- dataset generation;
- authorization-scope digest;
- compiler and artifact-format version;
- canonical V2 document and normalized route occurrences;
- candidate-to-occurrence bindings;
- resolved recipe and schema;
- resolved-recipe and output-contract digests;
- public output schema and emitted-column identities;
- per-output fingerprint;
- deterministic warnings; and
- creation and retention metadata.

The receipt does not persist optimized physical IR, AQL, bind variables, or
physical storage names. Those are request-scoped compiler products. Runtime
deterministically lowers the frozen resolved recipe again and verifies its
recipe digest, resolved schema digest, and output fingerprints. This
re-lowering is cheap and does not repeat semantic discovery or authoring
translation.

### Compile operation behavior

The compilation operation accepts authoring intent and an exact capability
snapshot. It validates, normalizes, lowers, resolves, optimizes, creates the
public bindings, persists the receipt, and returns the receipt ID.

Compilation is side-effect free with respect to user data and publication: it
does not materialize rows, create an active Explorer revision, or move an active
pointer. Persisting the immutable receipt is necessary and is not publication.

Compilation is idempotent. The same canonical intent, snapshot, scope,
generation, schema, receipt format, and compiler contract produces the same
compilation key and content-addressed receipt ID. Explicit compile/recompile
always rebuilds normalized intent; a compilation-key match is not treated as
proof that an old artifact remains reproducible. Arango uses immutable
`INSERT ... overwriteMode: "ignore"` rather than an updating UPSERT, then Loom
reads and re-lowers the stored artifact with the preview verifier before it can
return success.

The receipt must be stored before Loom returns a successful Builder
reconciliation. Returning success without a retrievable artifact recreates the
current race in which preview has to repeat the work.

### Compatibility with the current frontend

The existing frontend can use the boundary without owning compiler internals:

1. POST /builder compiles and persists the receipt.
2. The response includes the receipt ID and compiler-owned emitted columns.
3. Preview and publish submit that receipt ID.
4. Debounced compile plus compile-before-preview safely handles a missing or
   stale local receipt.

V1 has no production authoring route. It remains available only to migrate old
stored documents.

### Validity and retention

A content-addressed receipt never mutates into a different receipt. Execution
eligibility can still change:

- the caller may lose authorization;
- the pinned generation may be retired;
- the running Loom version may not support the artifact format;
- an operator may invalidate a compromised compiler build; or
- an unreferenced ephemeral receipt may be explicitly purged.

These cases produce explicit execution diagnostics. Loom must never silently
recompile the receipt against a newer generation or snapshot.

Receipts referenced by published immutable revisions are retained with those
revisions. Loom exposes receipt size/count statistics and explicit orphan
purge. Automatic TTL is intentionally deferred until deployed storage
measurements justify a policy.

### Concurrency behavior

Receipts also make debounced frontend concurrency harmless. If several Builder
requests are in flight, each successful request produces an artifact for its
own content. The frontend may ignore an older response, but the old response
cannot overwrite the newer artifact. Previewing a receipt always refers to one
exact intent.

## E. Preview by receipt

```text
POST .../authoring/v2/preview
  receiptId + outputId + optional limit
```

Preview does not accept fresh mutable intent.

### Current behavior

The production V2 route now loads the exact persisted receipt by project,
Explorer, and receipt ID. It does not accept an authoring bundle and does not
call the authoring compiler.

### Required preview flow

Receipt-based preview performs only:

1. Load the immutable receipt.
2. Verify that it belongs to the route's project and Explorer.
3. Re-authorize the caller against the pinned project, generation, and scope.
4. Validate the receipt ID, compilation key, format/compiler versions, resolved
   recipe digest, and output-contract digest.
5. Verify that outputId exists in the receipt.
6. Deterministically lower the frozen resolved recipe without catalog
   discovery.
7. Verify the resolved schema digest and per-output fingerprints.
8. Apply a bounded preview window to only the requested output.
9. Render and execute with server-owned authorization bind variables.
10. Return the public columns and rows recorded by the receipt.

It must not:

- read a new capability snapshot;
- normalize route occurrences;
- resolve candidate IDs;
- lower authoring intent;
- rediscover dynamic fields;
- rediscover or reinterpret semantic inputs; or
- select a different generation.

### Generation semantics

The receipt pins an immutable generation. Preview should remain reproducible
against that generation while it is retained and the caller remains authorized,
even after a newer generation becomes active. A generation switch must not
silently change an existing receipt.

If the pinned generation has been retired, Loom returns a specific
receipt-input-unavailable diagnostic. The frontend can then refresh Builder
capabilities and reconcile again.

Publication may use a stricter policy and require that the receipt generation
is still the expected active generation. Preview and publication do not need
identical staleness rules.

### Execution contract

The receipt already contains:

- output identity;
- public output schema;
- resolved semantic recipe;
- project, generation, and authorization-scope identity;
- resolved recipe/schema and output fingerprints; and
- deterministic diagnostics.

Preview rebuilds request-scoped physical IR and AQL because those are compiler
implementation details, then adds the bounded row limit. It does not redo
authoring translation or schema discovery.

Loom must enforce:

- default and maximum row limits;
- a ten-second request deadline;
- client cancellation;
- maximum query cost or approved plan class;
- output-specific execution rather than running every output;
- authorization predicates independent of client input; and
- a 32 MiB encoded-response limit with atomic response semantics.

The static plan gate runs before Arango execution and rejects a plan outside
the approved preview class. Representative generated plans are checked with
Arango `EXPLAIN`; passing the gate is not a promise that Arango will be
available at execution time. If the encoded response would exceed 32 MiB,
Loom returns `PREVIEW_RESPONSE_TOO_LARGE` without a partial row set. If the
deadline expires, it returns `PREVIEW_TIMEOUT`. Receipt-store dependency
failures use `RECEIPT_STORE_UNAVAILABLE`; those and timeouts are retryable.
`PLAN_TOO_EXPENSIVE` and `PREVIEW_RESPONSE_TOO_LARGE` require a smaller
request, while `CLIENT_CANCELED` is not a server outage.

Fiber/fasthttp does not propagate a browser disconnect through the handler's
context. Preserve `CLIENT_CANCELED` when an explicit cancellable context is
terminated, but assume an aborted browser request may continue until Loom's
ten-second deadline. Never classify an empty or truncated preview by matching
error text. The browser should retain the latest Builder document and branch
on stable diagnostic codes when a response is available.

Compilation validates lowering for every output, and representative integration
tests should render and run Arango EXPLAIN. Database execution can still fail
because Arango is unavailable, but it should not fail because Loom advertised a
traversal it could never lower.

### Response

The response preserves the frontend's public rows-and-columns shape and binds
it to the receipt:

~~~json
{
  "outputId": "out_...",
  "columns": [],
  "rows": [],
  "rowCount": 0,
  "receiptId": "receipt_..."
}
~~~

## F. Asynchronous publication

```text
POST /publications
  compilationId
  expectedActiveRevision

→ operationId
```

Publication should:

- materialize outputs;
- verify them;
- persist the immutable Explorer revision;
- atomically activate it;
- retain the previous active revision on any failure.

The UI should poll or subscribe to an operation with a clear stage.

## G. Typed diagnostic registry

Every diagnostic should define:

- stable code;
- stage;
- affected object ID;
- JSON path;
- user-facing message;
- technical details;
- retryable flag;
- recovery action.

Recovery actions might include:

- `REFRESH_CATALOG`;
- `REMOVE_ROUTE_STEP`;
- `CHANGE_ROW_ROOT`;
- `RESELECT_COLUMN`;
- `RECOMPILE`;
- `RETRY_PREVIEW`;
- `CONTACT_OPERATOR`.

No layer should classify errors by searching `err.Error()`.

### Diagnostics are a safety net

Diagnostics are not the primary mechanism for telling the frontend what is
selectable. If the capability snapshot is correct, normal traversal authoring
should not routinely produce unsupported row-grain, stale candidate, invalid
selector, or impossible route errors.

Failures still occur because of stale state, authorization changes, malformed
clients, query cost, dependency outages, and publication conflicts. Those
failures need one Loom-wide contract.

### Current Loom gap

Loom currently has two overlapping mechanisms:

- Explorer AuthoringError and AuthoringDiagnostic; and
- the more disciplined internal dataframe UserError contract.

The dataframe contract already provides stable codes, safe details, field
paths, retryability, cause preservation, and normalization without parsing
English text. The Explorer boundary partially bypasses it and sometimes
classifies compiler failures by searching error strings.

Authoring should use one registry and one adapter. Schema, compiler, planner,
renderer, Arango, and publication errors are normalized once. The original
wrapped cause is retained in server logs only.

### Wire contract

For traversal failures, the affected identity should be as specific as
possible:

- output ID;
- tab ID;
- route occurrence ID;
- edge ID;
- node ID;
- candidate ID;
- emission ID; or
- compilation receipt ID.

A consistent diagnostic looks like:

~~~json
{
  "severity": "error",
  "stage": "capability|intent|compile|preview|publish",
  "code": "INVALID_TRAVERSAL",
  "jsonPath": "$.documents[0].routeEdgeIds[2]",
  "object": {
    "kind": "routeEdge",
    "id": "e_..."
  },
  "message": "This relationship is no longer available from Observation.",
  "details": {
    "fromNodeId": "n_observation",
    "snapshotToken": "sha256:..."
  },
  "retryable": false,
  "recoveryAction": "REFRESH_CAPABILITIES",
  "requestId": "..."
}
~~~

Severity casing must be standardized. The current Go authoring response uses
uppercase values while frontend types expect lowercase values.

### Ordering for the current frontend

The current frontend normally displays only the first diagnostic's message and
code. Until that changes, Loom must:

- sort diagnostics deterministically;
- put the most actionable root cause first;
- make the first message understandable without hidden details;
- never expose a raw Go, Arango, or ClickHouse error as that message;
- attach the request ID to the first diagnostic; and
- avoid returning a generic wrapper when a specific semantic cause exists.

Loom should still return every diagnostic so the frontend can improve without
another backend redesign.

### Recovery actions

Recovery actions describe what state became invalid; Loom should not silently
rewrite user intent. The registry should distinguish at least:

- REFRESH_CAPABILITIES;
- REMOVE_ROUTE_STEP;
- CHANGE_ROW_ROOT;
- RESELECT_COLUMN;
- RECOMPILE;
- RETRY_PREVIEW; and
- CONTACT_OPERATOR.

### Error ownership

Each layer owns its semantic failures:

- capability compiler: incomplete snapshot, invalid resource or relationship,
  schema incompatibility;
- authoring normalizer: malformed occurrence or identity structure;
- compiler: invalid traversal, selector, cardinality, or plan cost;
- receipt store: missing or incompatible receipt;
- preview executor: timeout, cancellation, backend unavailable;
- publication: conflict, materialization failure, activation failure.

The HTTP adapter maps the normalized error to status and wire diagnostics. It
does not infer semantics from text.

Suggested status policy:

- 400 for malformed protocol or JSON;
- 401/403 for authentication or authorization;
- 404 for an absent Explorer, receipt, output, or retained generation;
- 409 for a stale snapshot, revision conflict, or incompatible receipt;
- 422 for well-formed intent violating a declared semantic rule;
- 429 for a cost or concurrency limit;
- 503 for a retryable dependency failure; and
- 500 for an unknown defect with a safe generic message.

The server log for every failure should include request ID, diagnostic code,
stage, project, Explorer, snapshot or receipt identity, and the full wrapped
cause. The client receives only the safe contract.

### Diagnostic invariants

Tests should guarantee:

- every public failure has a registered code;
- each code has one stable meaning and recovery action;
- no raw backend error reaches the response;
- retryable codes and HTTP statuses agree;
- JSON paths refer to real authoring fields;
- diagnostics are deterministically ordered;
- the first diagnostic remains useful to the current frontend; and
- typed dataframe errors survive the Explorer adapter without collapsing into
  INVALID_AUTHORING_INTENT.

## H. A simple frontend state machine

The frontend should primarily manage:

```text
CLEAN
DIRTY
SAVING
VALIDATING
INVALID
COMPILED
PREVIEWING
PUBLISHING
PUBLISHED
CONFLICTED
```

A catalog selection should either be executable or visibly disabled. The browser should not need to understand Loom’s schema resolver, semantic planner, physical lowering, Arango, ClickHouse, or release lifecycle.

---

# 13. What to keep, rewrite, and retire

## Keep

- generated FHIR schema metadata;
- generated and generic resource extraction;
- graph resource representation;
- immutable generation manifests and atomic activation;
- recipe semantic and physical compiler;
- AQL renderer;
- Arango query client;
- ClickHouse bundle publisher;
- immutable Explorer revisions;
- focused tests around compilation and failure-safe activation.

## Rewrite or consolidate

- Explorer authoring HTTP contract;
- capability catalog construction;
- error translation;
- draft ownership;
- compilation receipt lifecycle;
- preview/publication orchestration;
- generation-aware storage resolution;
- field enrichment scheduling.

## Retire

- unused legacy Explorer lifecycle/compiler paths;
- separately handwritten Go and TypeScript contract definitions;
- observation catalogs as direct authoring truth;
- endpoints that accept and recompile whole bundles for preview and publish;
- last-writer-wins activation;
- migration execution inside the server pod.

This can remain a modular monolith. It does not require splitting Loom into a dozen network services.

---

# 14. Recommended sequence

## Phase 0: Stabilize the existing interface

- Hide every non-concrete row root.
- Validate catalog candidates through the real selector/compiler logic before advertising them.
- Add `eligible` and `blockedReason`.
- Render all diagnostics, request IDs, stages, and recovery actions.
- Pass request IDs for preview and publication.
- Cancel superseded reconciliation calls.
- Stop silently omitting incomplete visible tables from validation.
- Deploy matching frontend/backend contract versions.

## Phase 1: Establish a generated shared contract

- Publish JSON Schema or OpenAPI for authoring documents and responses.
- Generate both Go validation fixtures and TypeScript types.
- Add an explicit protocol version and compiler/schema build digest.
- Fail closed when frontend and Loom do not support the same version.

## Phase 2: Introduce drafts and compilation receipts

- Persist drafts with CAS.
- Make compilation side-effect free.
- Preview exclusively by receipt.
- Invalidate receipts when draft or catalog snapshot changes.

## Phase 3: Make publication asynchronous and transactional

- Materialize from a receipt.
- Preserve the previous active release on failure.
- Expose publication stage and operation status.
- Make retries idempotent.

## Phase 4: Separate enrichment from ingestion

- Keep graph loading bounded.
- Run field profiling asynchronously.
- Use bounded sketches or top-K values rather than exact unbounded distinct sets.
- Never block generation activation on optional UI enrichment.

## Phase 5: Remove the old architecture

- Delete inactive V2 lifecycle/compiler code.
- Remove compatibility adapters once the hard cutover is proven.
- Reduce the Explorer store to clear draft, compilation, revision, publication, and active-pointer concepts.

---

# 15. Testing assessment

The focused Loom test suite passed across the server, Explorer, compiler, publication, catalog, and FHIR-schema packages.

The targeted frontend checks also passed:

- 7 Loom API/state tests;
- 2 Explorer contract/runtime tests;
- no-output TypeScript checks for both core and frontend packages.

Those tests confirm local behavior, not frontend/backend compatibility. Missing coverage includes:

- every advertised root compiles;
- every advertised relationship extends the route legally;
- every advertised candidate can be selected;
- all backend diagnostics render and identify their target;
- stale snapshot recovery preserves the draft;
- concurrent browser edits produce a conflict rather than overwrite;
- frontend/backend version skew fails clearly;
- preview and publication use the same compiled plan;
- generation activation changes authoring snapshots safely;
- invalid dependency responses such as `null` arrays cannot cross the contract;
- Arango or ClickHouse failures preserve the active Explorer revision.

A cross-repository conformance suite is more valuable here than adding more isolated unit tests.

---

# Final judgment

A radical restructuring is warranted, but the target is specific:

> Loom must stop exposing storage observations and compiler internals as a frontend authoring API.

The frontend is not fundamentally asking for something unreasonable. It wants to build a dataframe by selecting a row root, walking relationships, choosing columns, previewing, and publishing. Loom currently makes that workflow cross too many partially overlapping abstractions.

The desired boundary should be:

```text
The backend advertises only legal actions.
The frontend submits a small versioned intent.
Compilation produces an immutable receipt.
Preview and publish consume that receipt.
Every failure has a stable recovery action.
```

Until that boundary exists, fixing individual errors such as `qualification`, stale candidates, null emissions, route continuity, or unsupported selectors will continue to reveal the next failure underneath.
