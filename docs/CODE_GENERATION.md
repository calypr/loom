# Code generation

Loom checks generated code into Git so a normal build does not need a code
generator. Generator configuration and schemas are source files; everything
under `generated/` is produced or managed by a generator. Change an input and
regenerate instead of patching executor, model, schema, or FHIR output by hand.

The two gqlgen configurations live at the repository root:

- `gqlgen.yml` builds the graph and FHIR dataframe API.
- `gqlgen.clickhouse.yml` builds the published-dataframe reader API.

## Where generated code lives

Generated artifacts live outside `internal`; handwritten server behavior stays
under `internal`:

| Directory | Package | Why it lives there |
| --- | --- | --- |
| `generated/fhir/` | `fhir` | Public generated FHIR resource structs and validation helpers. |
| `generated/fhirschema/` | `fhirschema` | Raw generated schema tables consumed by the internal schema package. |
| `generated/graphqlapi/executor/` | `executor` | Primary gqlgen executable schema. |
| `generated/graphqlapi/model/` | `model` | Shared GraphQL input/output models. |
| `generated/graphqlapi/resolver/` | `resolver` | Primary gqlgen resolver bindings. |
| `generated/graphqlapi/clickhouse/executor/` | `executor` | Flat-reader executable schema. |
| `generated/graphqlapi/clickhouse/resolver/` | `resolver` | Flat-reader resolver bindings. |

The generated executors expose gqlgen's resolver interfaces. gqlgen requires
resolver receivers and preserved resolver implementations to share one Go
package, so its resolver directories also contain thin GraphQL adapter glue.
Application behavior remains in `internal/graphqlapi/query`,
`internal/graphqlapi/materialization`, and the internal dataframe services.
Database access and compiler logic do not belong in `generated/`.

Resolver implementations are the narrow exception to the no-edit rule: gqlgen
creates their package and method signatures, then preserves method bodies and
adjacent adapter helpers across regeneration. Edit transport mapping there when
needed, but do not move application services or storage logic into that package.

The handwritten GraphQL inputs and runtime wiring are deliberately separate:

| Directory | Responsibility |
| --- | --- |
| `internal/graphqlapi/schema/` | Handwritten primary GraphQL schema. |
| `internal/graphqlapi/clickhouse/schema/` | Handwritten flat-reader schema. |
| `internal/graphqlapi/` | HTTP handler and shared GraphQL error presentation. |
| `internal/graphqlapi/query/` | Arango graph and FHIR dataframe request services. |
| `internal/graphqlapi/materialization/` | Published-dataframe transport mapping. |
| `internal/graphqlapi/clickhouse/` | Flat-reader HTTP handler. |

## Sources of truth and outputs

| Input | Command | Generated outputs |
| --- | --- | --- |
| `schemas/graph-fhir.json` and `cmd/generate/` | `make generate-fhir` | `generated/fhir/*.go`, `generated/fhirschema/generated.go`, and `generated/graphqlapi/schema/fhir_schema.graphqls` |
| `internal/graphqlapi/schema/schema.graphqls`, generated FHIR SDL, and `gqlgen.yml` | `make generate-graphql` | Primary executor, models, and resolver bindings under `generated/graphqlapi/` |
| `internal/graphqlapi/clickhouse/schema/schema.graphqls` and `gqlgen.clickhouse.yml` | `make generate-graphql` | Flat-reader executor and resolver bindings under `generated/graphqlapi/clickhouse/` |

`generated/graphqlapi/executor/fhir_schema.generated.go` and
`generated/graphqlapi/executor/root_.generated.go` are
large because gqlgen emits executable dispatch code for every selectable field
in the typed FHIR schema. They are generated runtime code, not duplicate data
models or a second query engine.

`generated/graphqlapi/resolver/fhir_schema.resolvers.go` contains generated
field bindings that call shared helpers. It lets all JSON FHIR property names
remain selectable without hand-writing one resolver per field.

## Workflow

After changing the FHIR schema or its generator:

```bash
make generate-fhir
make generate-graphql
make graphql-check
git diff --check
```

`make generate-fhir` must run before `make generate-graphql`, because it
refreshes the generated FHIR GraphQL SDL consumed by gqlgen. Generated-file
drift is a failure: commit the resulting files with the input change.

After changing only a handwritten GraphQL schema, gqlgen configuration, or
resolver binding, run:

```bash
make generate-graphql
make graphql-check
git diff --check
```

To understand an executor, model, schema, or FHIR result, start at its input
above. Regeneration overwrites those artifacts; only the resolver adapter bodies
described above are preserved for handwritten transport mapping.
