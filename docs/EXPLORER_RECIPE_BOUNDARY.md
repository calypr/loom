# The Explorer recipe boundary

This document explains why the Explorer Builder sends authoring intent instead
of constructing Loom's complete native recipe. The short version is:

> The frontend chooses what the user wants. Loom decides how that request is
> interpreted, authorized, lowered, optimized, and executed.

The frontend could generate a syntactically valid recipe for simple cases. The
problem is that a Loom recipe is not merely a JSON formatting template. It is a
storage-neutral compiler input whose fields expand into schema rules, graph
traversal rules, authorization predicates, dynamic projections, physical IR,
and AQL.

For the request and endpoint contract, see
[Explorer authoring](EXPLORER_AUTHORING.md). For the complete compilation
sequence, see [Explorer compilation architecture](EXPLORER_COMPILATION_ARCHITECTURE.md).

## What the frontend owns

The V1 Builder owns authoring intent:

- which output the user is creating;
- which catalog node is the base and row node;
- which ordered catalog edges form the route;
- which opaque candidates are selected at which occurrences; and
- labels, ordering, visibility, filters, and charts for server-owned emissions.

For example:

```json
{
  "apiVersion": "loom.calypr.org/explorer-authoring/v1",
  "kind": "ExplorerAuthoringBundle",
  "project": "project-a",
  "explorerId": "patient-explorer",
  "document": {
    "kind": "ExplorerBuilderDocument",
    "output": { "id": "patient" },
    "baseNodeId": "node_patient",
    "rowNodeId": "node_condition",
    "routeEdgeIds": ["edge_patient_condition"],
    "routeOccurrences": [
      {
        "id": "condition_1",
        "index": 0,
        "nodeId": "node_condition",
        "incomingEdgeId": "edge_patient_condition"
      }
    ],
    "candidateIds": ["candidate_condition_status"],
    "candidateOccurrences": [
      {
        "candidateId": "candidate_condition_status",
        "occurrenceId": "condition_1"
      }
    ]
  }
}
```

This is durable authoring intent. It does not contain `expr`, `select`,
aliases, generated column names, physical collections, AQL, or authorization
paths.

## What Loom owns

Loom owns the translation from that intent to executable behavior:

```text
intent document
  -> authoritative catalog and snapshot resolution
  -> route and candidate resolution
  -> native recipe construction
  -> semantic and schema validation
  -> physical graph plan
  -> authorization and generation scope
  -> optimization
  -> parameterized AQL
```

Some values below are technically discoverable by a frontend. They are still
server-owned because they depend on authoritative state, security context, or
compiler implementation rules.

## 1. Selector paths and array semantics

### What the frontend sees

The V1 catalog gives the frontend an opaque candidate and useful display
metadata:

```json
{
  "candidateId": "candidate_observation_code_display",
  "nodeId": "node_observation",
  "fieldRef": "Observation.code.coding.display",
  "logicalType": "string",
  "filterable": true,
  "chartable": true
}
```

The catalog intentionally does not expose the internal recipe selector path.

### What the native recipe needs

The server may lower that candidate into:

```json
{
  "name": "c_<generated-id>",
  "fieldRef": "Observation.code.coding.display",
  "expr": {
    "select": "root.code.coding[].display"
  }
}
```

The `[]` is not decoration. It says that `coding` is repeated and that the
compiler must iterate it.

Depending on value mode and cardinality, Loom may need behavior equivalent to:

```aql
FOR coding IN (root.code.coding ? root.code.coding : [])
  FILTER coding.display != null
  RETURN coding.display
```

or:

```aql
FIRST(
  FOR coding IN (root.code.coding ? root.code.coding : [])
    FILTER coding.display != null
    RETURN coding.display
)
```

The compiler determines whether a selector is scalar or repeated, whether
arrays need flattening, how nulls are handled, and whether a direct scalar
access or a generated nested loop is safe. That behavior lives in selector
parsing, schema metadata, semantic typing, and AQL rendering.

A frontend could copy those rules, but it would then be implementing part of
the Loom compiler.

## 2. Field references are not complete execution selectors

`fieldRef` is provenance and catalog identity. It is not always enough to
construct the executable selector.

For example, a FHIR choice or nested repeated field may have distinctions such
as:

```text
Observation.value[x]
Observation.valueString
Observation.valueQuantity.value
DocumentReference.content[].attachment.extension[].url
```

The generated schema decides which paths exist, their logical type, whether
they are repeated, and which selector form the recipe compiler accepts.

For a traversal field, the selector also needs the correct lexical context:

```json
{ "select": "route_0.code.text" }
```

The meaning of `route_0` comes from the server-generated traversal scope. A
frontend-generated selector can be syntactically plausible while referring to
an alias that does not exist at that location.

## 3. Routes, traversal targets, and aliases

### User intent

The user selects a logical route:

```text
Patient --subject_Patient--> Condition
```

The V1 document represents that with node IDs, edge IDs, and occurrences.

### Native recipe

Loom may construct:

```json
{
  "traversals": [
    {
      "name": "subject_Patient",
      "alias": "route_0",
      "toResourceType": "Condition",
      "matchMode": "OPTIONAL",
      "fields": [
        {
          "name": "c_<generated-id>",
          "fieldRef": "Condition.status",
          "expr": { "select": "route_0.status" }
        }
      ]
    }
  ]
}
```

Some of those values can be derived from the catalog. The server still has to
verify that:

- the edge exists in the current snapshot;
- the edge connects to the current route node;
- the next edge begins at the previous edge's target;
- the occurrence index matches the ordered route;
- the terminal node equals `rowNodeId`; and
- the route depth is supported.

If the route is:

```text
Patient -> Patient -> Condition
```

then `Patient.id` may be valid at two different occurrences. A resource type
alone cannot tell Loom which one the candidate belongs to. V1 therefore has
explicit route occurrences and candidate-occurrence references.

## 4. Logical routes versus physical graph traversal

The authoring catalog describes logical relationships. Loom stores graph
relationships in `fhir_edge` with physical endpoint fields and indexed route
shapes.

The recipe may say:

```text
traverse subject_Patient to Condition
```

The physical compiler must decide how to implement that using the actual edge
layout:

```text
logical relationship
  -> physical _from/_to endpoint choice
  -> edge label predicate
  -> target resource type predicate
  -> correct graph index route
  -> target resource lookup
```

The frontend should not know the physical endpoint orientation, index names,
or storage-specific route proof. That is why the generated native recipe does
not need to carry a browser-supplied physical direction.

## 5. Aliases are lexical compiler scope

Aliases determine which expressions are legal.

This is valid:

```json
{
  "alias": "condition",
  "toResourceType": "Condition",
  "fields": [
    { "expr": { "select": "condition.code.text" } }
  ]
}
```

This is invalid if `specimen` is not in the current scope:

```json
{
  "alias": "condition",
  "toResourceType": "Condition",
  "fields": [
    { "expr": { "select": "specimen.code.text" } }
  ]
}
```

The semantic compiler tracks aliases, resource types, selector paths, and
expression types together. Server-generated aliases such as `route_0` avoid
making the frontend responsible for maintaining that lexical compiler scope.

## 6. Generated fields, emissions, and public columns

One selection passes through several identities:

```text
(candidateId, occurrenceId)
  -> emission ID
  -> native recipe field name
  -> public output column
```

For example:

```text
candidateId = candidate_patient_id
occurrence  = base
emissionId  = em_<hash(output, occurrence, candidate)>
fieldName   = c_<hash(candidate, occurrence)>
publicName  = c_<hash(emission)>
```

These identities connect the authoring document to presentation, compiled
configuration, output schema, persisted revisions, and materialized columns.

The frontend could copy the hashing algorithms, but then those internal
algorithms become a public frontend dependency. A server change to identity
generation would invalidate browser-authored documents.

That is why V1 presentation looks like:

```json
{
  "presentation": {
    "em_<server-owned-id>": {
      "label": "Patient ID",
      "visible": true
    }
  }
}
```

It does not look like:

```json
{
  "presentation": {
    "column": "c_abc123"
  }
}
```

The frontend presents an emission. Loom decides which concrete column that
emission becomes.

## 7. Authorization is runtime context, not recipe intent

The frontend can provide a project identity:

```json
{ "project": "project-a" }
```

It should not provide authorization paths. Loom obtains those from the
authenticated principal and request scope, then binds them during compilation.

The physical plan requires scope checks for every resource and relationship.
Conceptually, the generated AQL includes logic like:

```aql
FOR root IN @@patient_collection
  FILTER root.project == @project
  FILTER root.dataset_generation == @dataset_generation
  LET root_scope_allowed = AUTH_RESOURCE_PATH_ALLOWED(
    root.auth_resource_path,
    @auth_resource_paths,
    @auth_resource_paths_unrestricted
  )
  FILTER root_scope_allowed
```

Traversed edges and target resources receive equivalent checks. A browser
recipe that omits them may still look semantically valid while being unsafe to
execute. A browser that supplies them cannot be trusted to widen or alter the
scope.

## 8. Generation and catalog snapshots go stale

Consider:

```text
10:00  Builder fetches catalog for generation G1
10:02  ETL activates generation G2
10:03  Builder submits candidate IDs discovered from G1
```

The candidate IDs may still be well-formed. They may even look valid. But they
can now resolve to a different schema, edge set, or authorization scope.

The V1 snapshot token binds the catalog to project, generation, authorization
scope, nodes, edges, candidates, and completeness. Loom re-reads and validates
that snapshot during compilation. The correct result for stale state is a
typed conflict such as:

```text
409 STALE_CATALOG_SNAPSHOT
```

A frontend cannot guarantee consistency between catalog discovery and later
compile, preview, or publish.

## 9. Dynamic schema depends on actual data

Some native recipes discover columns from project data. For example, a
DocumentReference extension family may produce keys such as:

```text
source_path
sha256
sample_id
```

A native dynamic-column declaration may look conceptually like:

```json
{
  "name": "attachment_extensions",
  "source": {
    "select": "root.content[].attachment.extension[]"
  },
  "key": {
    "call": "last_segment",
    "args": [{ "select": "item.url" }]
  },
  "value": {
    "select": "item.valueString"
  },
  "maxColumns": 128
}
```

The compiler must then:

1. discover the actual keys;
2. freeze the allowed key set;
3. determine logical value types;
4. enforce the maximum column bound;
5. create deterministic physical projections;
6. create keyed-map lookups and bind variables; and
7. validate runtime key/type drift.

The result depends on:

```text
project + generation + data + authorization scope
```

A frontend can display a current key list, but it cannot make that list
authoritative for a later publication.

## 10. Recipe validation is semantic

Recipe validation checks more than JSON shape.

An expression must have exactly one operator:

```json
{ "select": "root.id" }
```

is valid, while this is invalid:

```json
{
  "select": "root.id",
  "literal": "also-present"
}
```

Other semantic checks include:

- selectors must resolve against the resource type in scope;
- aliases cannot shadow existing lexical bindings;
- traversal match modes must be supported;
- traversal depth must remain within compiler limits;
- dynamic sources must have the required array cardinality;
- dynamic item selectors must be valid scalar paths;
- output and field names must be stable recipe identifiers; and
- physical plans must contain required project, generation, and auth scope
  operations before AQL rendering.

The frontend could duplicate these rules, but it would then be maintaining a
second implementation of Loom's compiler semantics.

## 11. The physical plan is where storage and security meet

The native recipe may only say:

```text
root = Patient
traverse subject_Patient to Condition
select Condition.code.text
```

The physical plan expands that into operations such as:

```text
scan Patient collection
filter project
filter dataset generation
check root authorization
traverse fhir_edge
filter edge project/generation
check edge authorization
load Condition target
check target authorization
extract payload.code.text
construct row identity
project public columns
```

Only after that does the canonical AQL renderer produce parameterized AQL and
bind variables.

The frontend should not know collection names, edge indexes, bind-variable
names, payload extraction rules, scope-check ordering, row-identity rules, or
which physical optimizations are safe.

## Why this is a server boundary

The first authoring stage is not required because every individual value is
unknowable to a browser. It is required because the browser should not own or
be trusted with:

- authoritative catalog interpretation;
- snapshot and generation consistency;
- authorization scope;
- schema and cardinality semantics;
- generated executable identities;
- dynamic data-dependent schema;
- physical storage behavior; or
- compiler validation and optimization rules.

If the frontend generated the complete native recipe, it would become a second
Loom compiler. Loom would still need to validate and recompile that recipe for
authorization, snapshots, schema, and physical correctness.

The V1 boundary keeps the responsibilities clear:

```text
Frontend: user intent and presentation choices
Loom:    catalog resolution, recipe construction, compilation, authorization,
         physical planning, AQL, preview, and materialization
```

