# Authoring a default dataframer recipe

This is the practical introduction.  For the complete field-by-field language
reference, safe editing/runbook, dynamic-column semantics, default replacement
behavior, and a lossless `DocumentReference` attachment mapping, read the
[Dataframer recipe reference and operating manual](DATAFRAMER_RECIPE_REFERENCE.md).

If you are changing the shipped default, use that reference's safe editing
loop before deploying.  In particular, do not use wildcard scalar catalog
discovery to model FHIR extension URL/value pairs: define a targeted dynamic
key/value family instead.

A dataframer recipe tells Loom how to turn an authorized FHIR graph into one
or more flat ClickHouse datasets. It defines roots, outbound traversals,
selected fields, and bounded column discovery. It does not contain project
IDs, authorization paths, Arango collection names, AQL, ClickHouse table
names, or SQL.

This document describes the native recipe language used by repository, ETL,
and developer workflows. It is not the browser Builder contract. The Builder
sends the intent-only V2 document described in
[`EXPLORER_AUTHORING.md`](EXPLORER_AUTHORING.md); Loom lowers that document to
a native recipe and then runs the recipe compiler described here. Do not make
frontend code construct or repair recipe ASTs.

The deployed default is ordinary JSON loaded from
`server.dataframer.recipe`. It is required only when ClickHouse is enabled.

For direct recipe authoring, do not write a recipe from memory: discover the
loaded graph, prototype the shape through `runFhirDataframe`, and then promote
the proven shape into a persistent recipe. For Builder authoring, use the V2
REST contract instead of this native recipe format.

## Recommended workflow

1. Decide which public flat datasets are needed. Usually each Explorer index
   becomes one recipe output.
2. Run builder introspection for each root resource type.
3. Prototype one output at a time with `runFhirDataframe`.
4. Copy the closest output from the deployed default recipe and change only
   the root, fields, and traversals that were proven in step 3.
5. Change `translationVersion`.
6. Deploy the candidate recipe to a non-production Loom.
7. Run recipe validation, preflight, and a bounded preview.
8. Review the preflight column list before materializing into ClickHouse.

The production default for the Helm deployment lives in the separate
`gen3-helm` repository at:

```text
helm/loom/files/default-dataframer.json
```

That file is a working multi-output example and is the best starting point for
a related deployment.

## Migrating the Gen3 Tracker `meta dataframe` contract

Do not use a wildcard scalar catalog projection as the compatibility layer for
the legacy tracker output. The tracker flattened a `DocumentReference`
attachment into the public columns `contentType`, `url`, `size`, `hash`, and
`title`, and named attachment-extension columns from the final segment of
their URL (for example, `source_path` and `sha256`). A wildcard projection
instead exposes FHIR storage paths such as
`content_attachment_extension_valueUrl`; it also cannot preserve the legacy
extension key names.

Use explicit attachment fields and normalized dynamic keys. `columnPrefix: ""`
is intentional: it publishes a dynamic key directly rather than prepending
the dynamic-family name. Include the whole FHIR resource as an object column
when the migration must retain every source value, including repeated values
that the legacy flat dataframe could only overwrite.

```json
{
  "name": "DocumentReference",
  "rootResourceType": "DocumentReference",
  "rowGrain": "file",
  "collisionPolicy": "overwrite",
  "fields": [
    {"name": "resource", "expr": {"document": {"context": "root"}}},
    {"name": "contentType", "expr": {"select": "root.content[].attachment.contentType"}, "valueMode": "FIRST"},
    {"name": "url", "expr": {"select": "root.content[].attachment.url"}, "valueMode": "FIRST"},
    {"name": "size", "expr": {"select": "root.content[].attachment.size"}, "valueMode": "FIRST"},
    {"name": "hash", "expr": {"select": "root.content[].attachment.hash"}, "valueMode": "FIRST"},
    {"name": "title", "expr": {"select": "root.content[].attachment.title"}, "valueMode": "FIRST"}
  ],
  "dynamicColumns": [
    {
      "name": "legacy_identifier_keys",
      "columnPrefix": "",
      "source": {"select": "root.identifier[]"},
      "key": {"call": "last_segment", "args": [{"select": "item.system"}]},
      "value": {"select": "item.value"},
      "maxColumns": 64
    },
    {
      "name": "legacy_category_keys",
      "columnPrefix": "",
      "source": {"select": "root.category[].coding[]"},
      "key": {"call": "last_segment", "args": [{"select": "item.system"}]},
      "value": {"select": "item.display"},
      "maxColumns": 256
    },
    {
      "name": "attachment_extension_keys",
      "columnPrefix": "",
      "source": {"select": "root.content[].attachment.extension[]"},
      "key": {"call": "last_segment", "args": [{"select": "item.url"}]},
      "value": {
        "call": "coalesce_string",
        "args": [
          {"select": "item.valueString"}, {"select": "item.valueUrl"},
          {"select": "item.valueCode"}, {"select": "item.valueInteger"},
          {"select": "item.valueDecimal"}, {"select": "item.valueBoolean"},
          {"select": "item.valueDate"}, {"select": "item.valueDateTime"}
        ]
      },
      "maxColumns": 128
    }
  ]
}
```

The catalog freezes the normalized dynamic keys in the same scope as the
materialization. Thus an extension URL ending in `source_path` is frozen and
looked up as `source_path`, rather than as its full URL. Keep
`translationVersion` distinct from the previous non-compatible recipe.

## Step 1: define the public output contract

Before choosing selectors, write down the intended datasets and their row
grain:

| Output | Root | One row represents |
| --- | --- | --- |
| `ResearchSubject` | `ResearchSubject` | One study enrollment |
| `Specimen` | `Specimen` | One specimen |
| `DocumentReference` | `DocumentReference` | One file |

These names are public API:

- `outputs[].name` becomes the `output` component of the exact dataframe
  selector `(recipe, translationVersion, output)`.
- `fields[].name`, pivot names, and dynamic-column names contribute public
  column names.
- A traversal `alias` namespaces fields from the related resource.

Changing those names is a breaking change for Explorer configurations,
downloads, and saved queries. Keep names stable unless the consuming
configuration is changing at the same time.

`rowGrain` is a required semantic label. It documents the intended row shape;
it does not itself duplicate or group rows. Use `expand` when one resource
must deliberately produce multiple rows.

## Step 2: discover the graph that is actually loaded

Use the REST V2 authoring compiler and its server-owned catalog discovery. It
reports populated fields, route candidates, and valid relationships for the
selected generation. Do not infer relationship labels from FHIR property names
or manufacture browser selector IDs. See
[`EXPLORER_AUTHORING.md`](EXPLORER_AUTHORING.md).

For a second hop, select the first-hop target in the authoring request. For
example:

```text
DocumentReference --subject_Specimen--> Specimen
Specimen          --subject_Patient----> Patient
```

Both labels and target types must be confirmed by the catalog. A relationship
with no discovered candidates is not useful for that project even if the FHIR
schema allows it.

## Step 3: prototype before persisting

Use `runFhirDataframe` to test the same root fields, traversal steps, pivots,
and catalog projections against real data. Keep the limit small while
authoring.

The GraphQL input and persistent recipe use the same concepts:

| `FhirDataframeInput` | Persistent recipe |
| --- | --- |
| `rootResourceType` | `outputs[].rootResourceType` |
| `rootFields` | `outputs[].fields` |
| `rootFilters` | `outputs[].filters` |
| `rootPivots` | `outputs[].pivots` |
| `rootCatalogProjections` | `outputs[].catalogProjections` |
| `traverse` | `outputs[].traversals` |
| traversal `edgeLabel` | traversal `name` |
| traversal `alias` | traversal `alias` |
| `project`, `limit`, authorization | Runtime bindings; never stored |

The copy-paste GraphQL examples in
[the Quickstart](QUICKSTART.md#6-run-a-sample-dataframe-query) cover
introspection and dataframe execution.

## Minimal recipe

This is the smallest useful shape:

```json
{
  "recipeSchemaVersion": 1,
  "name": "aced-meta-default",
  "translationVersion": "my-deployment-2026-07-30.1",
  "outputs": [
    {
      "name": "Patient",
      "rootResourceType": "Patient",
      "rowGrain": "patient",
      "collisionPolicy": "overwrite",
      "fields": [
        {
          "name": "id",
          "expr": {"select": "root.id"}
        },
        {
          "name": "gender",
          "expr": {"select": "root.gender"}
        }
      ]
    }
  ]
}
```

Keep the bundle `name` stable when updating the server-owned default. Existing
automation commonly materializes `aced-meta-default` by name.
`translationVersion` should identify the configuration revision and must
change whenever the intended output contract changes.

## Worked traversal example

The following output produces one row per `DocumentReference`, retains rows
whose relationships are missing, and adds selected Specimen and Patient data.
The relationship labels are examples from BForePC and must be discovered for
the target project.

```json
{
  "recipeSchemaVersion": 1,
  "name": "aced-meta-default",
  "translationVersion": "bforepc-2026-07-30.1",
  "outputs": [
    {
      "name": "DocumentReference",
      "rootResourceType": "DocumentReference",
      "rowGrain": "file",
      "collisionPolicy": "overwrite",
      "fields": [
        {
          "name": "id",
          "expr": {"select": "root.id"}
        },
        {
          "name": "status",
          "expr": {"select": "root.status"}
        }
      ],
      "catalogProjections": [
        {
          "name": "file_fields",
          "includePaths": [
            "content[].attachment.title",
            "content[].attachment.contentType",
            "content[].attachment.size",
            "content[].attachment.url"
          ],
          "kinds": ["scalar"],
          "naming": "PATH",
          "valueMode": "FIRST",
          "maxColumns": 16
        }
      ],
      "dynamicColumns": [
        {
          "name": "identifier_by_system",
          "source": {"select": "root.identifier[]"},
          "key": {"select": "item.system"},
          "value": {"select": "item.value"},
          "maxColumns": 64
        }
      ],
      "traversals": [
        {
          "name": "subject_Specimen",
          "toResourceType": "Specimen",
          "alias": "specimen",
          "matchMode": "OPTIONAL",
          "fields": [
            {
              "name": "id",
              "expr": {"select": "specimen.id"}
            },
            {
              "name": "identifier",
              "expr": {
                "call": "first",
                "args": [
                  {"select": "specimen.identifier[].value"}
                ]
              }
            }
          ],
          "traversals": [
            {
              "name": "subject_Patient",
              "toResourceType": "Patient",
              "alias": "patient",
              "matchMode": "OPTIONAL",
              "fields": [
                {
                  "name": "id",
                  "expr": {"select": "patient.id"}
                },
                {
                  "name": "deceasedBoolean",
                  "expr": {"select": "patient.deceasedBoolean"}
                }
              ]
            }
          ]
        }
      ]
    }
  ]
}
```

The alias is lexical. Fields inside the Specimen traversal should select from
`specimen`; fields inside the nested Patient traversal should select from
`patient`. The reserved `root` context remains available for the original
DocumentReference. Dynamic key/value expressions use the reserved `item`
context.

`OPTIONAL` preserves the parent row and emits null related columns when the
relationship is absent. `REQUIRED` removes parent rows that have no matching
target. Use `REQUIRED` only when that row-loss behavior is intentional.

## Selecting values

An expression contains exactly one of:

- `select`
- `call` with `args`
- `literal`
- `document`

Common selectors:

```jsonl
{"select": "root.id"}
{"select": "root.identifier[].value"}
{"select": "patient.deceasedBoolean"}
```

`[]` means a repeated FHIR path. A repeated result needs an explicit
cardinality decision:

```json
{
  "name": "identifier",
  "expr": {
    "call": "first",
    "args": [{"select": "root.identifier[].value"}]
  }
}
```

Or use a field `valueMode`:

| Mode | Result |
| --- | --- |
| `AUTO` | Preserve scalar values; repeated values follow compiler defaults |
| `FIRST` | First value or null |
| `ALL` | All values |
| `DISTINCT` | Deduplicated values |

Useful calls include `first`, `all`, `distinct`, `coalesce`,
`coalesce_string`, `concat`, `join`, `reference_id`, `basename`, `uuid3`, and
`uuid5`. Unsupported calls and invalid argument counts fail recipe validation.

## Let the catalog discover columns

Manual recipes should name important compatibility columns explicitly and use
bounded discovery for broad or data-dependent families.

### Populated scalar fields

`catalogProjections` selects populated paths for the node's resource type:

```json
{
  "name": "populated_fields",
  "includePaths": ["*"],
  "excludePaths": ["text*", "contained*"],
  "kinds": ["scalar"],
  "naming": "PATH",
  "valueMode": "FIRST",
  "maxColumns": 256
}
```

Prefer narrow `includePaths` for a new recipe. A broad `["*"]` projection is
convenient but creates wide ClickHouse tables, larger AQL, and higher
materialization memory use.

`PATH` preserves the populated FHIR path in the column name.
`PATH_SUFFIX` keeps only the final path segment and is more likely to create
name collisions.

Catalog projections are local to their node. Adding one to the root does not
automatically include fields from traversed resources; add a projection to
each traversal that needs it.

### Key/value families

Use `dynamicColumns` for identifiers and general key/value arrays. For
schema-aware FHIR extensions, prefer `extensionColumns` on an output or
traversal: it recursively discovers nested `Extension` URLs, freezes
URL-to-value mappings and logical types, and emits canonical JSON strings for
mixed or complex values. `maxColumns` is required; an explicit empty
`columnPrefix` preserves bare names such as `source_path` and `sha256`.

Use `dynamicColumns` for identifiers, extensions, and similar arrays whose key
becomes part of the column name:

```json
{
  "name": "extension_by_url",
  "source": {"select": "root.extension[]"},
  "key": {"select": "item.url"},
  "value": {
    "call": "coalesce_string",
    "args": [
      {"select": "item.valueString"},
      {"select": "item.valueCode"},
      {"select": "item.valueInteger"},
      {"select": "item.valueDecimal"},
      {"select": "item.valueBoolean"},
      {"select": "item.valueDate"},
      {"select": "item.valueDateTime"}
    ]
  },
  "maxColumns": 128
}
```

When `columns` is omitted, Loom discovers distinct keys from the authorized,
active dataset generation and freezes that set for the materialization.
`maxColumns` is a safety bound, not a sampling limit. Resolution fails rather
than silently dropping keys when the bound is exceeded.

### Observation and CodeableConcept pivots

Use catalog-backed pivot discovery instead of enumerating observed code or
display values by hand:

```json
{
  "name": "observation_component_values",
  "discovery": {
    "path": "component[].code",
    "maxColumns": 256
  }
}
```

For a top-level Observation code/value pair:

```json
{
  "name": "observation_values",
  "discovery": {
    "path": "code",
    "maxColumns": 256
  }
}
```

The field catalog supplies the correct item source, column selector, value
selector, and fallbacks. If discovery reports no columns, verify the resource
type, pivot path, and loaded data instead of adding guessed selectors.

## Expansion and deterministic identity

Traversals add related columns to the parent row. They do not mean “one row
per related item.” Use `expand` only when a repeated element must create
multiple rows:

```json
{
  "name": "GroupMember",
  "rootResourceType": "Group",
  "rowGrain": "expanded",
  "collisionPolicy": "overwrite",
  "expand": {
    "from": {"select": "root.member[]"},
    "as": "member"
  },
  "identity": {
    "name": "id",
    "expr": {
      "call": "uuid5",
      "args": [
        {"literal": "aced-idp.org"},
        {"select": "root.id"},
        {
          "call": "reference_id",
          "args": [{"select": "member.entity.reference"}]
        }
      ]
    }
  },
  "fields": [
    {
      "name": "group_id",
      "expr": {"select": "root.id"}
    },
    {
      "name": "member_id",
      "expr": {
        "call": "reference_id",
        "args": [{"select": "member.entity.reference"}]
      }
    }
  ]
}
```

An expanded output needs a deterministic identity based on stable source
values. Do not use array position as identity.

## Validation and rollout

First check JSON syntax:

```bash
jq empty dataframer.json
```

Deploy the candidate to a development namespace without rebuilding Loom:

```bash
helm upgrade --install loom ./helm/loom \
  --namespace loom --create-namespace \
  --set-file dataframer.recipe=./dataframer.json
```

The chart mounts the file at `/etc/loom/dataframer.json`. Loom parses and
registers it during startup. A missing or structurally invalid recipe stops
startup when ClickHouse is enabled.

After the pod is ready, validate the registered recipe against a real project:

```graphql
mutation ValidateRecipe($name: String!, $project: String!) {
  validateDataframeRecipe(
    input: {
      name: $name
      bindings: {project: $project}
    }
  ) {
    name
    recipeDigest
    translationVersion
    outputs {
      name
      rootResourceType
      rowGrain
      fieldNames
      dynamicColumns
    }
  }
}
```

```json
{
  "name": "aced-meta-default",
  "project": "HTAN_INT-BForePC"
}
```

Inspect the resolved ClickHouse-facing columns before execution:

```graphql
query PreflightRecipe($name: String!, $project: String!) {
  preflightDataframeRecipe(
    input: {
      name: $name
      bindings: {project: $project}
    }
  ) {
    recipeDigest
    resolvedSchemaDigest
    sourceGeneration
    columns {
      output
      dynamicName
      name
      logicalType
      repeated
      nullable
    }
  }
}
```

Then execute a small preview:

```graphql
mutation PreviewRecipe(
  $name: String!
  $project: String!
  $outputs: [String!]
) {
  previewDataframeRecipe(
    input: {
      name: $name
      bindings: {project: $project}
      limit: 10
      outputs: $outputs
    }
  ) {
    recipeDigest
    outputs {
      name
      columns
      rowCount
      rows
    }
  }
}
```

```json
{
  "name": "aced-meta-default",
  "project": "HTAN_INT-BForePC",
  "outputs": ["DocumentReference"]
}
```

Only materialize after the preview row shape and preflight columns are
approved. Changing a recipe produces a new recipe digest; existing READY
materializations do not rewrite themselves and must be rematerialized.

## Common failures

| Error or symptom | Likely cause | Fix |
| --- | --- | --- |
| Unknown traversal or target | Label/type was guessed or is absent from the active generation | Use builder introspection for that parent resource |
| Selector is invalid | Path is not populated, has the wrong lexical alias, or has the wrong cardinality | Copy the discovered path and use `first`, `all`, or `valueMode` deliberately |
| Pivot matched no columns | Wrong pivot path or no matching data | Inspect `pivotFields`; do not invent static columns |
| Discovery exceeds `maxColumns` | The project contains more populated fields or keys than the recipe allows | Narrow the projection or raise the bound after reviewing table width |
| Dynamic key discovery was truncated | The ingest catalog did not retain a complete distinct-key set | Fix catalog discovery; do not accept a partial materialization |
| Duplicate output column | Two fields sanitize to the same name, often with `PATH_SUFFIX` | Use explicit names or `PATH` |
| Root rows disappear | A traversal was marked `REQUIRED` | Use `OPTIONAL` unless row removal is intended |
| Materialization uses excessive memory | Broad projections, many pivots, or many traversals created a very wide query | Reduce the output contract before increasing ClickHouse memory |
| Explorer still shows an old shape | The recipe changed but the old materialization remains active | Rematerialize and verify the new recipe and schema digests |

## Rules of thumb

- Start with one output and one traversal.
- Prefer explicit compatibility fields plus narrow catalog discovery.
- Use the exact relationship labels returned by introspection.
- Use `OPTIONAL` by default.
- Keep project and authorization data out of the recipe.
- Never put AQL, SQL, collection names, or ClickHouse table names in a recipe.
- Treat output and column names as public API.
- Increase `translationVersion` for intentional contract changes.
- Validate, preflight, and preview against representative data before
  materializing.
