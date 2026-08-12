# Dataframer recipe reference and operating manual

This is the field-by-field reference for Loom dataframer recipes.  Read the
[authoring guide](DATAFRAMER_RECIPES.md) first for the short workflow; use this
document when making a real change, reviewing one, or diagnosing an unexpected
column.

The goal of a recipe is to describe a stable, reviewable mapping from FHIR
resources and relationships to dataframe rows.  It is deliberately independent
of Arango collections, AQL, ClickHouse tables, or SQL.  That makes recipes
portable, but it also means a change to a selector, a traversal, or a dynamic
column is an API change for every dataframe consumer.

## The safe editing loop

Use this sequence for every default-recipe change.  Do not edit the running
database record by hand.

1. Start from the checked-in recipe.  The Helm deployment default is
   `helm/loom/files/default-dataframer.json` in the separate `gen3-helm`
   repository.  A Loom server receives its path through
   `server.dataframer.recipe`.
2. Record the current recipe name, `translationVersion`, expected output names,
   and a small set of representative FHIR resources.  Keep their old output as
   your comparison fixture.
3. Decide whether the change is additive or breaking.  Adding a new nullable
   column is normally additive.  Renaming/removing a column, changing its
   value meaning, changing `rowGrain`, changing a traversal from optional to
   required, or changing a dynamic key is breaking.
4. Make the smallest declarative change.  Prefer explicit `fields` and
   bounded `dynamicColumns`; do not reach for `includePaths: ["*"]` merely to
   discover an unknown value.
5. Format and parse before trying a deployment:

   ```bash
   jq empty helm/loom/files/default-dataframer.json
   git diff --check
   ```

   From the Loom repository, run the focused compiler tests when recipe
   semantics are involved:

   ```bash
   go test ./internal/dataframe/recipe ./internal/dataframe/recipe/schema \
     ./internal/dataframe/semantic ./internal/dataframe/compiler/lower
   ```

6. Submit the recipe to **Validate**.  Validation checks document shape,
   identifiers, expressions, types, and configured bounds, but it does not
   prove that your desired data exists in a project.
7. Run **Preflight** against the target project and dataset generation.  This
   resolves scoped catalog discovery and exposes the frozen output schema.
   Inspect every column name, especially a dynamic family.
8. Run **Preview** against known records and compare values as well as headers.
   Verify no expected value has silently become null and no unrelated row was
   created or removed.
9. Deploy the configuration, restart/roll the server, and confirm the
   registered recipe digest and preview.  See [Default-recipe replacement](#default-recipe-replacement-on-server-start) for why the name matters.

For a lossless migration, turn the old output contract into test cases before
editing: the same input must retain every former column and value unless the
change is consciously approved.  “The information still exists somewhere in a
raw FHIR document” is not equivalent to preserving the dataframe contract.

## Mental model

The diagram below is the lifecycle of one recipe.  The important distinction is
between *authoring* a logical recipe and *resolving* schema-dependent column
sets for a specific project/generation.

```text
JSON recipe
   │ parse + static validation
   ▼
logical recipe ── runtime bindings (project, generation, authorization) ──► catalog resolution
   │                                                                  │
   │                                                                  ▼
   │                                                     frozen physical column names
   ▼                                                                  │
typed semantic plan ◄────────────────────────────────────────────────┘
   │
   ▼
preview / materialization / published dataframe
```

`catalogProjections`, dynamic columns without a static `columns` list, and
pivots with `discovery` are intentionally resolved in the middle step.  Their
output column set can therefore differ across projects or generations.  Use a
static list whenever the schema must be identical everywhere.

## Top-level recipe object

Every stored recipe is a JSON object with this shape:

| Property | Required | Meaning | Change guidance |
| --- | --- | --- | --- |
| `recipeSchemaVersion` | Yes | Recipe document language version.  Current value is `1`. | Do not increment it for an ordinary recipe edit. |
| `name` | Yes | Stable identity of the recipe in the persistent registry. | Keep it unchanged to replace the deployed default; change it only to introduce another recipe. |
| `translationVersion` | Yes | Human/version-control label for the translation contract. | Change it on every meaningful mapping change; use a descriptive immutable value. |
| `fragments` | No | Named reusable fragments expanded before compilation. | Useful for genuinely repeated declarations; review expanded output too. |
| `outputs` | Yes | One or more named dataframe shapes. | Each output is independently rooted and materialized. |

A compact valid starting point is:

```json
{
  "recipeSchemaVersion": 1,
  "name": "my_recipe",
  "translationVersion": "2026-08-05-initial",
  "outputs": [
    {
      "name": "Patient",
      "rootResourceType": "Patient",
      "rowGrain": "resource",
      "fields": [
        {"name": "id", "expr": {"select": "root.id"}}
      ]
    }
  ]
}
```

Recipe and output identifiers must begin with a letter or underscore and then
contain only letters, digits, and underscores.  Use `patient_id`, not
`patient-id` or `patient.id`.

### Default-recipe replacement on server start

When ClickHouse is enabled, Loom reads the configured recipe file during server
startup, parses and canonicalizes it, calculates a digest, and looks up the
stored recipe by **`name`**.

| Existing stored recipe | Incoming recipe | Result |
| --- | --- | --- |
| No recipe of that name | Valid recipe | The server saves it. |
| Same name and same canonical digest | Valid recipe | No change. |
| Same name and a different digest | Valid recipe | The server replaces the stored default. |
| Invalid/unreadable file | Any | Startup reports a dataframer-recipe degradation; do not treat it as a successful rollout. |

Canonicalization means whitespace and JSON key order do not cause a replacement
by themselves.  A semantic change does.  `translationVersion` is part of the
recipe and should accurately communicate the semantic version you expect to
operate.  It is not a substitute for examining the output schema.

Keep the same `name` when changing the shipped default.  Giving an edited
default a new name creates a second registered recipe instead of updating the
one callers expect.

## Output: one dataframe shape

An output controls rows rooted at one FHIR resource type.

| Property | Required | Meaning |
| --- | --- | --- |
| `name` | Yes | Public dataframe/output name, unique in the recipe. |
| `rootResourceType` | Yes | FHIR resource type selected as `root` (for example `DocumentReference`). |
| `rowGrain` | Yes | Human-readable grain label (`resource`, `file`, `study_enrollment`, etc.).  It documents the contract; it is not a FHIR selector. |
| `fields` | No | Explicit named scalar or object projections. |
| `filters` | No | Predicates applied at this node. |
| `pivots` | No | Bounded code/key-to-column projections. |
| `aggregates` | No | Summaries of matching values. |
| `slices` | No | Bounded representative nested records. |
| `traversals` | No | Related resource nodes, optionally nested. |
| `expand` | No | Turns a repeated root expression into one row per element. |
| `identity` | No | Deterministic identity for an expanded row. |
| `dynamicColumns` | No | Bounded key/value families that become columns. |
| `catalogProjections` | No | Bounded discovery of scalar FHIR paths. |
| `collisionPolicy` | No | `error` (recommended), `overwrite`, or `coalesce` for physical-name collisions. |

Declare a normal field when you know the source and desired column name.  It is
the clearest and most stable form:

```json
{
  "name": "content_type",
  "expr": {"call": "first", "args": [
    {"select": "root.content[].attachment.contentType"}
  ]}
}
```

Do **not** select a repeated path without deciding its cardinality.  `first`
chooses one scalar; `all` returns the collection; `join` creates a string.  If
several attachment fields must stay paired, an independent `first` for each
field is only safe if the input has exactly one attachment (or the intended
first attachment is guaranteed).  Use `expand` or a separate child-shaped
output when attachment-level pairing is part of the contract.

### Fields

`fields` is an ordered array.  Every item has:

| Property | Required | Meaning |
| --- | --- | --- |
| `name` | Yes | Physical/public column name after normalization. |
| `expr` | Yes | An expression yielding the value. |
| `fieldRef` | No | Catalog/UI provenance only; it does not change execution. |
| `fallbacks` | No | Selector-only alternatives applied after `expr`. |
| `valueMode` | No | `AUTO`, `FIRST`, `ALL`, or `DISTINCT`; omitted means `AUTO`. |

`valueMode` is a projection policy, not a way to preserve tuple alignment.  A
field’s `FIRST` does not make other fields use the same array element.

| Value mode | Effect |
| --- | --- |
| `AUTO` | Compiler default based on expression/cardinality; use an explicit call when the desired behavior is important. |
| `FIRST` | One first value. |
| `ALL` | All selected values. |
| `DISTINCT` | All selected values with duplicates removed. |

### `collisionPolicy`

Use `error` (or omit the property when that is the deployment default) for new
work.  It stops an ambiguous recipe from publishing two sources to one column.

`overwrite` is specifically useful when reproducing an older flattening
contract that updated a row map in declaration order.  The later declaration
wins.  This is a compatibility behavior, so document the collision and keep
the declarations in intentional order.  `coalesce` is only appropriate when
the two sources represent the same logical value and null fallback is desired;
do not use it to hide unrelated values.

## Expression language

An expression is exactly one of `select`, `call`, `literal`, or `document`.
Do not combine forms in one JSON object.

### `select`: choose data

Selectors use a lexical context followed by a FHIR path:

| Context | Available where | Example |
| --- | --- | --- |
| `root` | Every output node | `root.status` |
| traversal alias | Inside that traversal and its children | `patient.identifier[].value` |
| `item` | Inside a dynamic-column source item | `item.system` |
| expansion alias | Inside an output with `expand` | `member.entity.reference` |

`[]` means “all elements of this FHIR array.”  The following selectors are
different:

```json
{"select": "root.identifier[].value"}
{"select": "root.identifier[0].value"}
```

The first is a collection; the second is a particular position.  Prefer a
meaningful key/value mapping over a positional index for FHIR arrays whenever
possible.

### `call`: transform, combine, or reduce

Calls have a name and argument expressions:

```json
{
  "call": "coalesce_string",
  "args": [
    {"select": "item.valueString"},
    {"select": "item.valueUrl"}
  ]
}
```

Frequently used calls are:

| Call | Use |
| --- | --- |
| `first`, `all`, `distinct` | Choose one, retain all, or deduplicate a selector/expression. |
| `coalesce`, `fallback` | First non-null compatible value. |
| `coalesce_string` | First non-null scalar primitive converted to string; ideal for FHIR extension choice values. |
| `concat`, `join` | Combine strings; `join` takes a collection and delimiter. |
| `reference_id` | Convert `ResourceType/id` reference text to its id portion. |
| `last_segment`, `basename`, `path_segment` | Derive a stable segment from a URI/path.  Useful for a dynamic extension key. |
| `sanitize_name`, `sanitize_graphql_name` | Normalize a value for a physical/schema-safe name. |
| `uuid3`, `uuid5` | Deterministic identity from a namespace and values. |
| `cast` | Explicit typed conversion where supported. |

The expression checker enforces function arity and compatible types.  A URL
extension is usually selected as a string-like primitive; do not wrap an object
or repeated array in `coalesce_string`.

### `literal` and `document`

Use a literal for a constant:

```json
{"literal": "unknown"}
```

Use a document reference when the contract needs the original FHIR object
rather than a scalar leaf:

```json
{"document": {"context": "root"}}
```

`document` preserves the object in a named field such as `resource`; it does
not flatten all nested fields into columns.

## Traversals: related FHIR resources

A traversal adds fields from a related resource.  It is evaluated through the
FHIR graph; its recipe does not name a database edge table.

```json
{
  "name": "subject_Patient",
  "toResourceType": "Patient",
  "alias": "patient",
  "matchMode": "OPTIONAL",
  "fields": [
    {"name": "patient_id", "expr": {"select": "patient.id"}}
  ]
}
```

| Property | Required | Meaning |
| --- | --- | --- |
| `name` | Yes | Relationship label.  It may be reused if aliases differ. |
| `toResourceType` | Yes | Required FHIR destination type. |
| `alias` | No | Lexical name used in selectors; make it explicit for readability. |
| `from` | No | Optional explicit reference expression when the relationship needs disambiguation. |
| `matchMode` | No | `OPTIONAL` (default) retains an unmatched root; `REQUIRED` removes it. |
| all node features | No | Traversals can themselves contain fields, filters, pivots, aggregates, slices, dynamic columns, catalog projections, and nested traversals. |

`OPTIONAL` versus `REQUIRED` affects row inclusion, so it is a breaking change
unless an excluded root was already impossible.  The default recipe’s
`DocumentReference -> Specimen -> Patient` chain is an example where nested
traversals attach context without changing the root output identity.

## Dynamic columns: controlled key/value flattening

### `extensionColumns`: typed URL-keyed Extension values

For recipes that need a stable, loss-aware mapping of FHIR `Extension`
values, use `extensionColumns` on either an output or a traversal. `source`
must be a schema-valid repeated `Extension` selector and `maxColumns` is
required. Discovery walks nested `extension[]` arrays and freezes each URL's
normalized final segment (for example `source_path` and `sha256`). An explicit
empty `columnPrefix` publishes those names without a family prefix.

When discovery observes one primitive `value[x]` kind for a URL, the resulting
column retains that nullable logical type. URLs with mixed or complex values
are represented as canonical JSON strings. The frozen URL, source path, value
selector, and logical type are included in the resolved schema digest.

```json
{
  "name": "attachment",
  "columnPrefix": "",
  "source": {"select": "root.content[].attachment.extension[]"},
  "maxColumns": 16
}
```

`dynamicColumns` remains available for general key/value arrays and retains
its existing behavior.

### Author-controlled keyed columns

`dynamicColumns`, `extensionColumns`, and pivots accept an optional
`columnMode`:

- omitted or `DISCOVER` preserves the legacy behavior and freezes every key
  found by scoped catalog discovery;
- `SELECTED` makes `columns` authoritative. An empty or omitted `columns`
  list therefore emits no columns for that family.

The builder uses `SELECTED` only in project-authored drafts. Server-owned
legacy/default recipes remain discovery-driven unless explicitly changed.
Selected families work identically on an output root and on nested traversal
nodes.

The `dataframeRecipeColumnCandidates` GraphQL query accepts the authored
recipe, output name, traversal alias path, and optional dataset generation.
It returns stable, paginated candidates with their raw FHIR identity, public
column name, native recipe patch path, type/cardinality, population, examples,
selected state, and blocking completeness diagnostics. Callers must fetch all
pages and must not publish while `completeness.complete` is false.

### Published schema finalization

Preflight and preview report the conservative candidate schema produced by
catalog discovery. During publication, discovered columns that are missing or
null in every staged row are removed (empty repeated arrays are also empty);
`false`, zero, and empty strings count as populated. Authored and Loom-owned
columns are always retained. The published execution metadata, ClickHouse
table, Explorer schema, and API responses all use this retained schema.

Published `schemaDigest` values use the versioned final-schema algorithm over
the recipe digest, scope digest, dataset generation, output order, and ordered
logical column contracts. Physical table names and discovery provenance are
excluded. Newly materialized outputs therefore may have a different digest
from their preflight candidate, and an existing publication changes only after
normal rematerialization.

Use a dynamic column family when FHIR represents repeated key/value metadata
and each known key should become its own column.  This is the correct tool for
identifiers, category coding, and attachment extensions.  It is **not** a
wildcard scalar projection.

### How a family becomes physical columns

For each `source` item:

1. `key` produces a key (or, if no key is provided, the item/source’s catalog
   key is used where supported).
2. Loom normalizes that key to a valid physical name.
3. `value` produces the cell value.
4. The configured static keys or scoped discovery freezes the allowed columns.
5. At execution, only those frozen columns are populated.

| Property | Required | Meaning |
| --- | --- | --- |
| `name` | Yes | Internal family name; must be unique at its node. |
| `columnPrefix` | No | Public prefix.  Omit it for `name_normalized_key`; set `""` for the normalized key alone. |
| `source` | Yes | Repeated expression yielding items. |
| `key` | No | Expression evaluated for every `item`; normally scalar and string-like. |
| `value` | No | Expression evaluated for every `item`. |
| `columns` | No | Explicit allowed raw keys; no discovery occurs. |
| `maxColumns` | No | Bound for discovery.  Choose the narrowest safe number. |

The difference between omitted and empty `columnPrefix` is intentional:

```json
{"name": "extension", "source": {"select": "root.extension[]"}}
```

publishes `extension_source_path`, while:

```json
{"name": "extension", "columnPrefix": "", "source": {"select": "root.extension[]"}}
```

publishes `source_path`.  Empty prefix is important when reproducing the old
`gen3_tracker` flattened contract.

### Static versus discovered keys

Use a static `columns` list if a contract must be stable, even if a particular
project does not currently contain every key:

```json
{
  "name": "attachment_extensions",
  "columnPrefix": "",
  "source": {"select": "root.content[].attachment.extension[]"},
  "key": {"call": "last_segment", "args": [{"select": "item.url"}]},
  "value": {"call": "coalesce_string", "args": [
    {"select": "item.valueString"},
    {"select": "item.valueUrl"},
    {"select": "item.valueCode"},
    {"select": "item.valueInteger"},
    {"select": "item.valueDecimal"},
    {"select": "item.valueBoolean"},
    {"select": "item.valueDate"},
    {"select": "item.valueDateTime"}
  ]},
  "columns": ["source_path", "sha256"]
}
```

With that declaration the pair

```text
http://aced-idp.org/fhir/StructureDefinition/source_path = s3://.../file.ome.tiff
http://aced-idp.org/fhir/StructureDefinition/sha256      = a202ed...
```

becomes:

```text
source_path = s3://.../file.ome.tiff
sha256      = a202ed...
```

No unrequested attachment leaf is discovered.  This preserves the relationship
between a specific extension URL and its value, unlike separately discovered
columns such as `...extension_url`, `...extension_valueString`, and
`...extension_valueUrl`.

If keys are intentionally project-specific, omit `columns` and set a positive
`maxColumns`.  Loom performs **targeted discovery only at that source**.  It
uses the key expression when freezing the names, so
`last_segment(item.url)` freezes `source_path` and `sha256`, not the full URL.
Inspect Preflight before accepting this schema: a new extension URL in another
generation can legitimately create a new column, up to the limit.

### Extension values and losslessness

FHIR extensions use mutually exclusive `value[x]` properties.  Do not select
only `valueString` if the source uses `valueUrl` or other valid alternatives.
For a string dataframe contract, use `coalesce_string` in deliberate precedence
order, as in the prior example.  Add more alternatives only after deciding how
they should be represented; blindly flattening objects or complex extensions is
not lossless.

For exact type preservation, publish separate explicit columns or preserve the
extension/document object in a `document` field.  A string conversion is
lossless for the text but not for the original FHIR primitive type.

### Avoiding collisions

The same normalized key can appear in identifier systems, category systems,
and extension URLs.  With `columnPrefix: ""`, all of them share the same
physical namespace.  Either:

* use distinct explicit prefixes (`identifier_`, `category_`, `attachment_`),
  which is safest for new schemas; or
* use `collisionPolicy: "overwrite"` only when reproducing a documented legacy
  map-update order and test the winning source/value.

Sanitization can also make different raw keys collide.  Review the **physical
column names** reported by Preflight, not just raw input URLs.

## Catalog projections: discovery, not a lossless mapper

`catalogProjections` asks Loom’s scoped field catalog for populated scalar FHIR
paths, then materializes each selected path as a column.  It is useful for
exploration and broad data profiling; it is not the preferred solution for a
stable, lossless migration contract.

```json
{
  "name": "selected_scalars",
  "includePaths": ["content[].attachment.contentType", "content[].attachment.size"],
  "kinds": ["scalar"],
  "naming": "PATH",
  "valueMode": "FIRST",
  "maxColumns": 8
}
```

| Property | Meaning |
| --- | --- |
| `name` | Internal projection name. |
| `includePaths` | One or more catalog path patterns.  Avoid `*` in an operational default. |
| `excludePaths` | Optional exclusions after inclusion. |
| `kinds` | Catalog kinds to allow, commonly `scalar`. |
| `naming` | `PATH` uses the full normalized path; `PATH_SUFFIX` uses the tail and risks collisions. |
| `valueMode` | How a repeated selected path is represented. |
| `maxColumns` | Required upper bound, 1 through 512. |

Wildcard scalar discovery is crude for FHIR arrays: it produces independent
columns for sibling leaves and cannot encode which leaf values came from the
same array element.  In particular, it cannot represent extension URL/value
pairs safely.  Replace a wildcard with explicit fields and targeted dynamic
families before calling a migration lossless.

## Pivots

A pivot is a bounded code/value mapping.  Use it where a FHIR code identifies
a measurement or component whose value belongs in a named column.  A static
pivot supplies `columnExpr`, `valueExpr`, and a non-empty `columns` list.  A
catalog-backed pivot supplies a `discovery` object, from which Loom resolves
the concrete columns and selectors.

```json
{
  "name": "observation_values",
  "columnExpr": {"select": "observation.code"},
  "valueExpr": {"select": "observation.value"},
  "columns": ["hemoglobin", "platelet_count"]
}
```

`discovery.maxColumns` and static `columns` are mutually exclusive.  The limit
is 256.  As with dynamic columns, review frozen columns in Preflight; a pivot
is a schema feature, not an unbounded bag of values.

## Filters

Filters are typed predicates placed on outputs, traversals, aggregates, or
representative slices.  A filter has a selector string, an operator, optional
array quantifier, and typed values.

```json
{
  "select": "root.status",
  "operator": "EQUALS",
  "values": [{"kind": "CODE", "code": {"code": "current"}}]
}
```

| Operator | Values | Allowed value kinds |
| --- | --- | --- |
| `EQUALS`, `NOT_EQUALS`, `IN` | one (`IN`: one or more) | all declared kinds |
| `EXISTS`, `MISSING` | none | n/a |
| `CONTAINS_TEXT` | one | `STRING` |
| `GT`, `GTE`, `LT`, `LTE` | one | `INTEGER`, `DECIMAL`, `DATE`, `DATE_TIME` |

The available kinds are `STRING`, `CODE`, `BOOLEAN`, `INTEGER`, `DECIMAL`,
`DATE`, and `DATE_TIME`.  A `CODE` value may carry `system`, `code`, and
`display`, but `code` is required.  For an array selector, set `quantifier` to
`ANY`, `ALL`, or `NONE` rather than assuming scalar behavior.

Filters affect which data exists in a dataframe; treat their change as a
breaking data-contract change and test record counts.

## Aggregates and representative slices

Aggregates summarize a node:

| `operation` | Needs `expr`? | Meaning |
| --- | --- | --- |
| `COUNT` | No | Number of matches. |
| `EXISTS` | No | Whether a match exists. |
| `COUNT_DISTINCT` | Yes | Number of distinct expression values. |
| `DISTINCT_VALUES` | Yes | Distinct values. |
| `MIN`, `MAX` | Yes | Minimum/maximum expression value. |

An aggregate can use `where` to filter its own input and `valueMode` to shape
its result.  `COUNT` and `EXISTS` must not have an `expr`.

A representative slice is a bounded nested selection:

```json
{
  "name": "recent_related",
  "limit": 5,
  "fields": [
    {"name": "id", "expr": {"select": "observation.id"}},
    {"name": "status", "expr": {"select": "observation.status"}}
  ]
}
```

`limit` is required and must be 1 through 1000.  Give it a deterministic
selection/filter contract before treating it as a stable user-facing result.

## Expansion and identity

An output normally has one row at its root grain.  `expand` changes that to
one row for every element of a repeated root expression.  Supply `identity` so
each expanded row has a durable, deterministic key.

```json
{
  "name": "GroupMember",
  "rootResourceType": "Group",
  "rowGrain": "expanded",
  "expand": {"from": {"select": "root.member[]"}, "as": "member"},
  "identity": {
    "name": "id",
    "expr": {"call": "uuid5", "args": [
      {"literal": "calypr.org"},
      {"select": "root.id"},
      {"call": "reference_id", "args": [{"select": "member.entity.reference"}]}
    ]}
  },
  "fields": [
    {"name": "group_id", "expr": {"select": "root.id"}},
    {"name": "member_id", "expr": {"call": "reference_id", "args": [{"select": "member.entity.reference"}]}}
  ]
}
```

Changing the expansion source, alias, or identity expression changes row
identity/cardinality.  Treat it as breaking and check for duplicate identity
values in Preview.

## Fragments

Fragments are reusable declarations stored in the top-level `fragments`
library.  Loom expands them before validating the effective recipe, then stores
the standalone canonical recipe.  They reduce repeated JSON but can obscure
the actual schema, so use them only for a clear repeated pattern and inspect
the expanded/preflight result during review.

Fragments do not change the lifecycle rules: their expanded content affects the
digest and an edited default with the same recipe name replaces the stored one.

## Worked lossless DocumentReference attachment mapping

For a `DocumentReference` with one or more `content[].attachment` objects,
write the known attachment fields explicitly and map extensions by their URL
key.  The shipped default uses this explicit key/value approach with bounded
targeted discovery; the following static-key form is the strictest possible
contract when these two keys are the only supported ones.

```json
{
  "name": "DocumentReference",
  "rootResourceType": "DocumentReference",
  "rowGrain": "file",
  "collisionPolicy": "overwrite",
  "fields": [
    {"name": "id", "expr": {"select": "root.id"}},
    {"name": "resource", "expr": {"document": {"context": "root"}}},
    {"name": "contentType", "expr": {"call": "first", "args": [{"select": "root.content[].attachment.contentType"}]}},
    {"name": "url", "expr": {"call": "first", "args": [{"select": "root.content[].attachment.url"}]}},
    {"name": "size", "expr": {"call": "first", "args": [{"select": "root.content[].attachment.size"}]}},
    {"name": "hash", "expr": {"call": "first", "args": [{"select": "root.content[].attachment.hash"}]}},
    {"name": "title", "expr": {"call": "first", "args": [{"select": "root.content[].attachment.title"}]}}
  ],
  "dynamicColumns": [
    {
      "name": "attachment_extensions",
      "columnPrefix": "",
      "source": {"select": "root.content[].attachment.extension[]"},
      "key": {"call": "last_segment", "args": [{"select": "item.url"}]},
      "value": {"call": "coalesce_string", "args": [
        {"select": "item.valueString"}, {"select": "item.valueUrl"},
        {"select": "item.valueCode"}, {"select": "item.valueInteger"},
        {"select": "item.valueDecimal"}, {"select": "item.valueBoolean"},
        {"select": "item.valueDate"}, {"select": "item.valueDateTime"}
      ]},
      "columns": ["source_path", "sha256"]
    }
  ]
}
```

This does not use wildcard scalar discovery.  It defines the key as the last
path segment of the extension URL and maps that key to the extension’s actual
value.  For unknown-but-allowed extensions, delete `columns`, set an intentional
`maxColumns`, and use Preflight to approve the discovered keys before rollout.

The explicit `first` attachment fields mirror historical one-file-per-document
behavior.  If a source may contain multiple attachments and each must be
represented independently, this shape is insufficient; use `expand` at the
attachment level or design a new attachment-grain output.  Do not claim
losslessness based solely on having many sibling scalar columns.

## What to review in a pull request

Use this checklist before approving a recipe change:

- [ ] The JSON parses with `jq empty`; no unrelated recipe file changes are mixed in.
- [ ] `name` is unchanged for a default replacement, and `translationVersion` explains the mapping change.
- [ ] Every new/changed public column has a source, cardinality decision, and expected type.
- [ ] Every dynamic family has a bounded static `columns` list or a justified `maxColumns` and scoped discovery source.
- [ ] No `includePaths: ["*"]` was added to a production migration mapping.
- [ ] `columnPrefix` is deliberately omitted, non-empty, or explicitly empty; reviewers checked the resulting physical names.
- [ ] All possible FHIR `value[x]` alternatives needed by the contract are handled.
- [ ] Any `overwrite` collision is documented and its declaration order is tested.
- [ ] Traversal `matchMode`, filters, expansion, and identity changes have row-count/identity tests.
- [ ] Validate, Preflight, and Preview results were compared to representative existing output.
- [ ] The Helm/chart deployment uses the intended file and the resulting server registration/digest was confirmed.

## Troubleshooting guide

| Symptom | Likely cause | What to inspect/fix |
| --- | --- | --- |
| Recipe is rejected before execution | Schema, name, expression, function type, or bounded-limit validation failed. | Read the JSON path in the error; check expression form and allowed identifier syntax. |
| A field is null though FHIR visibly has data | Wrong lexical alias, wrong FHIR path, repeated selector without reduction, or incorrect `value[x]`. | Preview that resource; test the selector and include all expected value variants. |
| `source_path` is absent | Key source was not scoped to attachment extensions, the key transform differs from frozen names, or the value only exists as `valueUrl`. | Use `root.content[].attachment.extension[]`, `last_segment(item.url)`, and `coalesce_string` including `item.valueUrl`. |
| Full URL-named columns appear | `columnPrefix`/key transform is missing or the dynamic schema was frozen before the transform. | Set the key explicitly and inspect Preflight frozen physical names. |
| Columns such as `...extension_url` and `...extension_valueUrl` appear but are not paired | Wildcard catalog projection flattened array leaves independently. | Replace it with the attachment-extension dynamic family. |
| A changed default did not take effect | Wrong file mounted, server was not rolled, ClickHouse registration is disabled, or a different recipe name was used. | Verify `server.dataframer.recipe`, pod config/mount, server logs, recipe name, and registered digest. |
| Server still uses an old recipe after a restart | Incoming canonical JSON was actually identical, or the recipe file did not change in the deployed chart. | Compare mounted file and recipe digest; do not rely on whitespace/comment changes. |
| Preflight has extra/missing dynamic columns | Discovery is generation/project scoped, a key changed, or the `maxColumns` bound truncated the set. | Compare raw keys, transformed keys, the scoped catalog, and a static `columns` contract. |
| A later value unexpectedly wins | `collisionPolicy: "overwrite"` intentionally follows declaration order. | Rename/prefix the families or document and test the required order. |
| Related root rows disappeared | A traversal or filter became `REQUIRED`, or a node filter excludes them. | Compare Preview counts before/after; restore `OPTIONAL` when unmatched roots should survive. |

## Where implementation behavior lives

These source files are useful when the reference and observed behavior appear
to disagree:

| Concern | Source |
| --- | --- |
| JSON document types | `internal/dataframe/recipe/document_types.go` |
| Document, field, filter, pivot, aggregate, and traversal validation | `internal/dataframe/recipe/document_validation.go`, `validation_rich.go`, `dynamic_validation.go` |
| Catalog and transformed dynamic-key discovery | `internal/dataframe/recipe/schema/` |
| Expression type checking | `internal/dataframe/expression/` |
| Semantic column naming and collision handling | `internal/dataframe/semantic/` |
| Physical compilation | `internal/dataframe/compiler/lower/` |
| Default registration/replacement | `internal/dataframe/recipe/exec/persistent.go` and `internal/server/server.go` |

The implementation is the final authority for a newly introduced feature, but
the safe path is still: express the desired output contract in an explicit
recipe, Validate, Preflight, Preview, and retain a regression fixture.
