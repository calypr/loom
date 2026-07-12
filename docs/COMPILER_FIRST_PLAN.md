# Compiler-First FHIR Graph Lowering and AQL Optimization Plan

## 1. Purpose

This is the primary implementation program for Loom.

Loom's durable value is not its current browser page, job API, or export
wrapper. Its durable value is the compiler that turns a user-oriented FHIR
dataframe request into correct, authorization-safe, efficient AQL over an
observed FHIR graph.

The product should be built outward from this boundary:

```text
dataframe intent
  -> typed semantic request
  -> FHIR graph logical plan
  -> cardinality-aware relational/dataframe plan
  -> optimized physical AQL plan
  -> generated AQL + bind variables
  -> Arango EXPLAIN validation
  -> execution / preview / export
```

The compiler must be useful without a frontend. A frontend or future language
assistant should only have to produce the typed request and display validation,
plan explanation, and results.

This plan refactors the earlier 20-gap roadmap. Compiler work happens first.
Jobs, recipe persistence, Elasticsearch, deployment polish, and a larger
frontend are downstream consumers and must not dictate the compiler design.

## 2. Existing Compiler Backbone

This is not a greenfield compiler.

### Existing request and validation path

- `graphqlapi/schema.graphqls` defines fields, traversals, pivots,
  aggregates, slices, and value modes.
- `graphqlapi/dataframe` maps and resolves GraphQL input.
- `internal/dataframe/validation.go` validates fields and traversals against
  populated catalog records and generated schema metadata.
- `fhirschema/generated.go` contains generated FHIR definitions and
  traversals.
- `fhirschema/schema.go` resolves fields, selectors, traversals, and
  pivot families.

### Existing lowering path

- `internal/dataframe/planner.go` converts the public traversal tree to named
  sets and derived fields.
- `internal/dataframe/traversal_rules.go` classifies the currently optimized
  traversal tuples.
- `internal/dataframe/lowered_types.go` defines named sets, derived fields, and
  representative slices.
- `internal/dataframe/document_reference_semantics.go` defines specialized file
  summary behavior.

### Existing AQL compilation path

- `internal/dataframe/lowered_compile.go` emits the optimized named-set AQL.
- `internal/dataframe/compile_fields.go` emits traversal, selection, pivot, and
  aggregate expressions.
- `internal/dataframe/selectors.go` parses the current selector subset.
- `internal/dataframe/query_runtime.go` executes AQL through a callback cursor.

### Existing optimizations worth preserving

The current implementation already performs meaningful work:

- shares one root-neighbor traversal when several Patient child resource types
  use the same edge label
- filters the shared neighbor set into resource-specific named sets
- reuses named sets for multiple fields and aggregates
- unions DocumentReference routes before file-summary classification
- classifies DocumentReference payloads once for compatible selectors
- hydrates ResearchStudy through direct `DOCUMENT` lookup when required
- groups compatible pivots so one pivot map can serve multiple projections
- filters requested pivot columns through bind variables
- uses `LIMIT 1` for existence checks
- pushes project and auth-resource-path predicates into root and traversal AQL
- uses bind variables for user values and traversal labels/types
- validates against populated fields and references before compilation

Those behaviors need parity tests before the compiler is reorganized.

## 3. Current Compiler Gaps

### Semantic limitations

- Root lowering is limited to `Patient`.
- Traversal roles are a hardcoded subset of generated traversal metadata.
- A structurally valid root-only or simple one-hop request can be rejected
  because it does not trigger the specialized optimized profile.
- Row grain is implicit. The planner does not formally distinguish selecting
  arrays from exploding rows, aggregating related resources, or changing the
  output entity.
- FHIR reference direction, multiplicity, choice types, extensions,
  terminology, and repeated values do not have one complete semantic model.
- DocumentReference behavior is embedded as a special family rather than an
  optimizer rule over typed semantics.

### Intermediate representation limitations

- `logicalNode` is still close to GraphQL input and does not express joins,
  filters, grouping, row identity, or cardinality explicitly.
- `NamedSet` and `DerivedField` combine physical execution decisions with
  semantic operations.
- Set modes are tracked through string-keyed maps.
- Important behavior depends on string constants and sanitized variable names.
- There is no structured physical AQL AST or operation graph.
- There is no stable compiler explain artifact between the request and AQL.

### Filter and expression limitations

- The selector grammar is intentionally narrow.
- Predicate support is primarily existence and equality/contains behavior.
- Typed comparisons, code/system matching, date ranges, null semantics, and
  array quantifiers are incomplete.
- Filter pushdown is not represented as an optimizer decision.
- Some compiler paths treat invalid selector parsing as impossible after
  validation rather than carrying typed validated expressions.
- Internal raw-expression support is difficult to reason about and should not
  be reachable from untrusted product input.

### Physical AQL limitations

- AQL is assembled directly as formatted strings.
- `UNIQUE`, `FLATTEN`, and `MERGE` are used broadly and can eagerly materialize
  large arrays per root row.
- Traversal deduplication policy is not cost-based and is sometimes unconditional.
- Repeated selections can rescan the same set or payload path.
- Filters cannot consistently move before set materialization.
- Projection pruning does not remove unused data from specialized summaries.
- There is no choice between traversal, indexed lookup, precomputed rollup, or
  alternative join shapes based on observed statistics.
- Root `SORT`/`LIMIT` supports preview but is not a complete stable cursor or
  filtered-query strategy.

### Optimization evidence limitations

- Tests mostly validate generated query fragments and selected result paths.
- There is no systematic corpus of logical-plan and physical-plan goldens.
- There is no automated Arango `EXPLAIN` inspection.
- There are no plan budgets for scanned rows, peak memory, intermediate arrays,
  or execution time.
- There is no optimizer comparison against an unoptimized correct reference
  plan.
- `META/` is not yet used as a formal compiler benchmark corpus.

## 4. Compiler Definition of Done

The compiler is ready to support the product when it can:

1. accept any supported root/grain represented by generated FHIR metadata
2. validate every field, traversal, filter, aggregate, and pivot before AQL
3. model row identity and cardinality explicitly
4. produce a correct generic plan for every advertised supported request
5. apply optional optimizations without changing results
6. generate AQL only through typed validated physical operations
7. keep project, dataset generation, and authorization predicates on every
   applicable read/traversal
8. explain why a traversal, aggregate, or pivot was chosen
9. report unsupported semantics through stable reason codes
10. show benchmark and `EXPLAIN` evidence for important plan families
11. preserve result parity between generic and optimized plans
12. stream rows without materializing the complete dataframe in Loom

## 5. Compiler Work Packages

The compiler program is divided into ten work packages. These replace the old
single large planner gap.

---

# CP0: Compiler Corpus, Reference Semantics, and Baselines

## Objective

Create the executable oracle against which every compiler refactor and
optimization is judged.

## Implementation

1. Inventory the resource types, populated fields, pivot candidates, and
   relationship tuples in `META/`.
2. Record the current GraphQL input, normalized builder, lowered sets, AQL,
   bind variables, output columns, and result rows for representative queries.
3. Cover these current plan behaviors:
   - root fields
   - shared Patient neighbor set
   - Patient to Condition
   - Patient to Specimen
   - Specimen/Group to DocumentReference
   - DocumentReference summary classification
   - ResearchSubject to ResearchStudy hydration
   - Observation pivot
   - count, count-distinct, exists, and distinct-values
   - representative slices
   - multi-auth-path and unrestricted scope
4. Add minimal synthetic fixtures only for semantics missing from `META/`.
5. Define output comparison rules:
   - stable row key
   - scalar equality
   - array order or order-insensitive semantics
   - sparse pivot columns
   - null versus absent
6. Add a generic/reference execution mode or test evaluator. It may be slower,
   but it must favor obvious correctness over optimization.
7. Add compiler benchmark commands using `META/` and configurable larger data.
8. Capture baseline Arango `EXPLAIN` plans and execution statistics.

## Deliverables

- `conformance/compiler/`
- compiler fixture schema
- logical/result goldens
- benchmark harness
- baseline report

## Tests and gates

- current compiler outputs are reproducible
- generated and generic ingestion produce equivalent graph inputs
- every later optimization runs result parity against the reference mode

## Parallelism

May run in parallel with read-only CP1/CP2 design, but its comparison contract
must freeze before those packages implement new IR.

---

# CP1: Typed Dataframe and FHIR Semantic IR

## Objective

Replace compiler behavior inferred from nested GraphQL structs and strings with
a typed, backend-independent semantic request.

## Implementation

1. Define a `SemanticPlan` or equivalent in a new planner package.
2. Represent:
   - project/generation/auth scope
   - requested row grain
   - root resource type
   - semantic nodes and relationships
   - fields/projections
   - typed filters
   - aggregates
   - pivots
   - representative slices
   - output names and types
3. Introduce typed IDs for nodes, fields, relationships, and output columns.
4. Resolve raw GraphQL/recipe input into this IR once.
5. Store validated selectors as typed paths, not reparsed strings.
6. Keep source locations so errors point to the user's field/filter.
7. Define stable unsupported-reason and validation codes.
8. Make the IR serializable for golden tests and developer explain output.
9. Do not include AQL variable names, named-set choices, or optimizer hints.
10. Adapt the existing public builder into the semantic IR without breaking the
    current API during migration.

## Ownership

- new `internal/dataframe/planner/semantic/` or equivalent
- adapters from `graphqlapi/dataframe`

## Tests and gates

- deterministic semantic-plan goldens
- all current supported builders resolve without semantic loss
- malformed/unsupported input fails before physical lowering
- no backend/AQL types leak into the semantic IR

## Parallelism

One contract owner. CP2 and CP3 may contribute requirements but cannot define
parallel competing semantic types.

---

# CP2: Generated FHIR Graph Semantics

## Objective

Turn existing generated schema and traversal metadata into the authoritative
compiler knowledge base instead of maintaining a hardcoded Patient tuple list.

## Implementation

1. Inventory what `fhirschema/generated.go` already records:
   - definitions
   - properties/kinds
   - traversals
   - direction
   - multiplicity
   - backreferences
2. Define compiler-facing semantic APIs over `fhirschema`:
   - resolve field and result kind
   - resolve relationship/direction
   - determine source/target multiplicity
   - identify reference/choice/extension paths
   - identify pivot-compatible code/value families
3. Replace tuple recognition as the support source with generated traversal
   lookup plus explicit optimizer capabilities.
4. Retain domain-specific aliases or preferred paths as optional semantic rules,
   not proof that a relationship exists.
5. Define how extensions are addressed:
   - generic URL/value selectors
   - friendly aliases layered above them
6. Define terminology-aware field semantics using system/code/display.
7. Add FHIR date, dateTime, instant, quantity, Coding, CodeableConcept, and
   Reference type descriptors needed by filters and output typing.
8. Add generator changes only when the active graph schema contains information
   that current generated metadata omits.
9. Regenerate through `make generate-fhir` and preserve deterministic output.
10. Test every traversal and generated field represented by `META/`.

## Ownership

- `fhirschema/`
- `cmd/generate/` only when necessary
- no copied FHIR structs or handwritten replacement schema

## Tests and gates

- generated metadata reproducibility
- every observed `META/` relationship resolves through the semantic API
- optimizer support is distinct from schema existence
- generated/generic ingestion parity remains intact

## Parallelism

Can proceed beside CP3 after CP1's node/field/relationship identifiers freeze.

---

# CP3: Row Grain and Cardinality Algebra

## Objective

Make "one row per" a formal compiler property rather than a frontend hint.

## Implementation

1. Define grain identity for:
   - Patient
   - Specimen
   - DocumentReference/File
   - Condition/Diagnosis
   - Observation
   - ResearchSubject/Study enrollment
2. Define relationship cardinalities:
   - required one
   - optional one
   - repeated/many
   - unknown/observed-many
3. Define projection modes:
   - scalar
   - first/representative
   - repeated array
   - distinct array
   - aggregate
   - pivot
   - explode to rows
4. Require every semantic plan to have one stable row identity.
5. Define duplicate semantics when multiple graph paths reach the same resource.
6. Define how joins affect row multiplicity.
7. Reject accidental Cartesian products.
8. Use generated multiplicity plus observed fanout statistics; never replace
   formal semantics with observed data alone.
9. Emit row-expansion and ambiguous-grain warnings.
10. Add algebraic tests for nested one-to-many paths.

## Tests and gates

- exact row counts for compiler fixtures
- stable row identity across generic/optimized plans
- no implicit row explosion
- duplicate path convergence has defined behavior

## Parallelism

May proceed with CP2 after CP1's core semantic IR freezes. Must finish before
generic lowering and cost optimization are considered stable.

---

# CP4: Typed Expression and Filter Compiler

## Objective

Implement safe, FHIR-aware fields and filtering independently of frontend
wording and raw AQL.

## Implementation

1. Define a typed expression tree:
   - path access
   - literal/bind value
   - boolean composition
   - comparison
   - existence/missing
   - array quantifiers
   - terminology match
   - date/range comparison
2. Support initial operators:
   - equals/not-equals
   - in/not-in
   - exists/missing
   - contains text
   - greater/less comparisons
   - between/date range
3. Define `ANY`, `NONE`, and only later `ALL` repeated-value semantics.
4. Define missing versus null versus empty array.
5. Match Coding/CodeableConcept by system and code when available; display is
   presentation, not the canonical identity.
6. Normalize FHIR date/dateTime/instant comparison and timezone policy.
7. Type-check every operator against CP2 descriptors.
8. Lower all user values to bind variables.
9. Make raw AQL expressions inaccessible from public input.
10. Compile filters into logical predicates before physical AQL.
11. Support filter pushdown metadata: which node/path owns each predicate.
12. Add fuzz tests for selector and expression parsing.

## Tests and gates

- result-based operator tests
- repeated-value quantifier tests
- code/system tests
- null/missing tests
- injection tests
- auth predicates cannot be removed by filter rewrites

## Parallelism

Expression types need one owner. Operator implementations can be split after
the AST and type rules freeze.

---

# CP5: Correct Generic FHIR Graph Lowering

## Objective

Guarantee a correct physical plan for every advertised semantic request before
applying special optimizations.

## Implementation

1. Define a generic logical-to-physical lowering using CP2 graph metadata.
2. Support arbitrary validated root resource types.
3. Represent physical operations:
   - root scan
   - indexed root lookup
   - graph traversal
   - filter
   - project
   - distinct/deduplicate
   - aggregate/group
   - pivot
   - explode
   - slice
   - union
   - direct document lookup
4. Propagate project, generation, and auth scope through every operation.
5. Choose traversal direction from generated semantics plus observed
   `fhir_edge` layout. The current catalog's parent-to-child contract is
   inbound; do not mistake FHIR reference direction for physical AQL direction.
6. Preserve row identity and grain from CP3.
7. Implement root-only and simple one-hop requests; they must not require a
   specialized optimization profile.
8. Detect cycles and cap graph depth.
9. Produce a typed `PhysicalPlan`, not AQL strings.
10. Preserve the current Patient-case-assay planner as an optimization path for
    parity comparison, not the only working path.

## Tests and gates

- all six initial grains lower generically
- simple/root-only requests work
- every traversal carries scope predicates
- generic plan results match reference semantics
- cycles and unsupported directions fail clearly

## Parallelism

One core lowerer owner. After physical operation interfaces freeze, workers may
add root/grain adapters in separate files.

---

# CP6: Aggregation, Pivot, and Representative-Selection Engine

## Objective

Make dataframe shaping a first-class, correct subsystem rather than scattered
string templates.

## Implementation

1. Define aggregate semantics and output types for:
   - count
   - count distinct
   - exists/any
   - distinct values
   - min/max where required
2. Define whether nulls participate in every aggregate.
3. Define pivot inputs:
   - key expression
   - value expression
   - duplicate key/value policy
   - requested versus discovered columns
   - sparse output policy
4. Support CodeableConcept and Observation code/value pivot families through
   CP2 metadata.
5. Preserve the existing compatible-pivot grouping optimization.
6. Prevent high-cardinality unbounded pivot materialization.
7. Define representative selection with explicit ordering and stable tie-break.
8. Distinguish representative selection from arbitrary `FIRST`.
9. Define array output encoding independently of CSV/Elasticsearch.
10. Add generic and optimized implementations for parity testing.

## Tests and gates

- duplicate pivot-key cases
- multiple values per key
- sparse/missing pivot keys
- high-cardinality rejection/warning
- aggregate null behavior
- deterministic representative rows
- current `META/` pivot parity

## Parallelism

Aggregate, pivot, and representative-selection implementations can run in
parallel after their shared semantic contracts freeze.

---

# CP7: Typed AQL Physical IR and Code Generation

## Objective

Separate plan construction from AQL rendering so optimization operates on typed
nodes instead of formatted strings.

## Implementation

1. Define an AQL-oriented physical AST or strongly typed operation graph.
2. Represent:
   - collection scans
   - traversals
   - LET bindings
   - loops
   - filters
   - sorts/limits
   - collect/group
   - projections
   - subqueries
   - bind variables
3. Generate collision-free internal variables without user-controlled names.
4. Make collection/resource identifiers come only from validated schema
   metadata.
5. Make every user value a bind variable.
6. Add a scope verifier that walks the physical plan and proves required
   project/generation/auth predicates exist.
7. Render deterministic AQL for snapshot and cache use.
8. Attach source semantic node and optimizer-rule provenance to physical nodes.
9. Keep an escape hatch for audited internal expressions only; mark them
   explicitly and prohibit product input from creating them.
10. Port existing named-set code generation incrementally behind this boundary.

## Tests and gates

- deterministic render tests
- bind-variable completeness
- identifier injection tests
- scope-verifier negative tests
- current AQL result parity during incremental port

## Parallelism

One AST/renderer contract owner. Individual operation renderers can be split
after the node interfaces freeze.

---

# CP8: AQL Optimization Passes

## Objective

Build explicit, independently testable optimizer passes that reduce graph work,
intermediate materialization, and repeated payload extraction.

## Required passes

### Projection pruning

- remove unused fields from summaries and slice projections
- avoid building payload-derived values that no output/filter consumes

### Predicate pushdown

- move root filters before traversal
- move child predicates into traversal subqueries
- apply resource type, label, project, generation, and auth filters at the
  earliest valid point

### Traversal sharing

- generalize the existing shared Patient-neighbor optimization
- share identical traversal prefixes across selected fields/aggregates
- split only when predicates or cardinality semantics differ

### Common-subexpression elimination

- reuse identical selector extraction
- reuse identical distinct sets
- reuse compatible pivot maps
- reuse DocumentReference classification and study lookup

### Set fusion and materialization control

- fuse filter/project operations where doing so preserves reuse
- avoid `FLATTEN` when direct iteration suffices
- avoid `UNIQUE` when schema/cardinality proves uniqueness
- choose `COLLECT`/distinct strategy deliberately
- avoid creating a full union when downstream consumers can stream branches

### Lookup selection

- choose graph traversal, indexed collection lookup, direct `DOCUMENT`, or
  precomputed rollup using semantics and evidence
- keep specialized paths optional and result-equivalent

### Aggregate/pivot pushdown

- count or test existence without materializing complete node arrays
- filter pivot keys before value aggregation
- share grouped pivot computation

### Limit and cursor pushdown

- apply preview/export boundaries only where they preserve requested semantics
- use stable grain identity for keyset cursors
- never limit a child set before an aggregate unless semantics request it

## Optimizer framework

1. Each rule has:
   - stable name
   - preconditions
   - transformation
   - correctness tests
   - estimated effect
2. Apply rules to a fixed point or controlled sequence.
3. Record applied/skipped rules in plan explain.
4. Allow rules to be disabled for parity/debugging.
5. Compare optimized results against CP5 generic plans.
6. Add plan-size and intermediate-cardinality estimates.

## Tests and gates

- one test family per rule
- multi-rule interaction tests
- generic/optimized result parity
- scope verifier runs after all rewrites
- no rule is accepted based only on shorter AQL text

## Parallelism

After the optimizer framework and physical IR freeze, individual passes are
highly parallelizable. Each worker owns additive rule files and tests; one
optimizer lead owns pass ordering and central registration.

---

# CP9: Arango-Aware Costing, Indexing, EXPLAIN, and Performance Gates

## Objective

Make optimization evidence-driven against real Arango behavior and the actual
FHIR data distribution.

## Implementation

1. Add an Arango `EXPLAIN` adapter that captures:
   - plan nodes
   - indexes used
   - estimated cost/items
   - optimizer rules
   - warnings
2. Add execution profiling for benchmark mode:
   - runtime
   - scanned/full-scan counts
   - peak/estimated memory where available
   - result rows
   - intermediate cardinalities when observable
3. Define required index families for:
   - root project/generation/auth/key scans
   - edge traversal and label/type/scope constraints
   - direct resource ID/reference lookup
   - any scalar index used for pushdown
4. Review existing edge indexes against emitted traversal direction and filter
   order. Do not add indexes speculatively.
5. Feed resource counts, field coverage, distinct counts, and relationship
   fanout into a small cost model.
6. Start with rule-based thresholds. Do not build an elaborate cost-based
   optimizer until measurements require it.
7. Define performance budgets for plan families:
   - root-only filtered table
   - one-hop projection
   - multi-hop file manifest
   - high-volume Observation filter
   - pivot
   - aggregate-only query
8. Benchmark generic and optimized plans on `META/` and a scaled dataset.
9. Reject optimizer changes that regress important plans without an explicit
   documented tradeoff.
10. Publish a compiler performance report from CI/scheduled runs.

## Tests and gates

- expected critical index used
- no unexpected full collection scan for indexed benchmark cases
- output parity
- p50/p95 timing and scanned-item budgets
- memory/intermediate-size budgets
- repeatable benchmark commands

## Parallelism

Index analysis, EXPLAIN tooling, cost inputs, and benchmark execution can be
parallelized after CP7 produces stable physical plans.

---

# 6. Compiler Work-Package Dependency Graph

```text
CP0 Corpus/baseline
  ├──────────────┐
  v              v
CP1 Semantic IR  CP2 FHIR semantics
  │              │
  ├──────┬───────┘
  v      v
CP3 Grain/cardinality    CP4 Expressions/filters
  └──────────┬───────────┘
             v
       CP5 Generic lowering
             │
             ├────────> CP6 Aggregate/pivot engine
             │
             v
       CP7 Physical AQL IR
             │
             v
       CP8 Optimizer passes
             │
             v
       CP9 EXPLAIN/cost/index/performance
```

CP6 can begin its semantic contracts after CP1-CP4, but optimized code generation
must integrate through CP7/CP8.

## 7. Compiler-First Parallel Waves

### Compiler Wave A: Oracle and contracts

- Worker A: CP0 corpus and result comparison
- Worker B: CP1 semantic IR contract
- Worker C: CP2 generated FHIR semantic APIs
- Worker D: current AQL/EXPLAIN baseline tooling

Gate:

- semantic IDs and result comparison freeze
- current supported results reproducible
- no replacement FHIR model introduced

### Compiler Wave B: Semantics

- Worker A: CP3 grain/cardinality
- Worker B: CP4 expression/filter AST and typing
- Worker C: CP6 aggregate semantic contract
- Worker D: CP6 pivot semantic contract
- Worker E: conformance fixtures for all above

Gate:

- typed semantic plan can express the six product families
- every operation has defined null/cardinality/output semantics

### Compiler Wave C: Correct lowering

- Worker A: CP5 generic lowering core
- Worker B: additive root/grain adapters after core freeze
- Worker C: CP7 physical AQL IR/renderer core
- Worker D: scope-verifier and bind/identifier safety
- Worker E: generic/reference parity suite

Gate:

- every advertised query has a correct generic plan
- root-only/simple queries work
- current Patient results remain correct

### Compiler Wave D: Optimizer

After CP7 freezes, parallel workers implement:

- projection pruning and predicate pushdown
- traversal sharing and prefix reuse
- common-subexpression elimination
- set fusion/materialization control
- aggregate/existence pushdown
- grouped pivot optimization
- lookup/rollup selection, including a proven inbound/outbound direction choice
- cursor/limit pushdown

One optimizer lead owns rule registration, ordering, and integration.

Gate:

- every rule has generic/optimized parity
- scope verifier passes after rewrites
- plan explain lists applied rules

### Compiler Wave E: Arango performance

- EXPLAIN/index worker
- cost/statistics worker
- benchmark execution worker
- large Observation/pivot stress worker
- regression and performance-report worker

Gate:

- important plan families meet written budgets
- optimizer decisions have Arango evidence
- slow paths are visible in explain/metrics

## 8. What Moves Later

The following former gaps become smaller downstream work because the compiler
owns the difficult semantics:

### Reduced product facade

- recipe templates become presets that construct semantic requests
- capability API delegates to compiler validation/capabilities
- frontend asks grain, columns, and filters, then displays compiler explain
- recipe persistence stores normalized compiler input

### Reduced preview/export work

- preview adds limits/cursor around compiled physical plans
- export consumes the compiler row stream
- NDJSON, CSV, and Elasticsearch are row sinks, not separate dataframe engines

### Deferred infrastructure

These remain necessary for a development service but should not block compiler
quality:

- durable load/export jobs
- immutable dataset generations
- saved recipe CRUD
- artifact storage
- Elasticsearch retries
- readiness, metrics, and runbooks

Implement only the minimum dataset identity, authorization context, catalog
statistics, and execution harness required to build and measure the compiler.

## 9. Compiler Release Gate Before Product Expansion

Do not invest in a larger frontend or broad service machinery until:

- CP0-CP7 are complete
- at least the first CP8 optimizer passes are complete
- Patient, Specimen, File, Diagnosis, Observation, and Study Enrollment grains
  have correct generic plans
- typed filters cover the initial product conversations
- pivots and aggregates have explicit tested semantics
- generic and optimized results match
- project/auth predicates are mechanically verified
- `META/` compiler benchmarks are repeatable
- Arango `EXPLAIN` output is available to developers
- current specialized Patient behavior is either preserved as a rule or beaten
  by the new generic/optimized plan

At that point the product layer becomes relatively thin and can be built with
far less risk.
