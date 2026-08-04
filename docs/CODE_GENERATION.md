# Code generation

Loom checks generated code into Git so a normal build does not need a code
generator. Generator configuration and schemas are source files; everything
under `generated/` is produced or managed by a generator; resolver packages
live under `internal/api/graphql`. Change an input and regenerate instead of
patching executor, model, schema, or FHIR output by hand.

The two gqlgen configurations live at the repository root:

- `gqlgen.yml` builds the graph and FHIR dataframe API.

## Where generated code lives

Generated artifacts live outside `internal`; handwritten server behavior stays
under `internal`:

| Directory | Package | Why it lives there |
| --- | --- | --- |
| `generated/fhir/` | `fhir` | Public generated FHIR resource structs and validation helpers. |
| `generated/fhirschema/` | `fhirschema` | Raw generated schema tables consumed by the internal schema package. |
| `generated/graphql/graph/executor/` | `executor` | Primary gqlgen executable schema. |
| `generated/graphql/graph/model/` | `model` | Shared GraphQL input/output models. |
| `internal/api/graphql/graph/resolver/` | `resolver` | Primary gqlgen resolver bindings. |

The generated executors expose gqlgen's resolver interfaces. gqlgen requires
resolver receivers and preserved resolver implementations to share one Go
package, so its resolver directories also contain thin GraphQL adapter glue.
Application behavior remains in `internal/api/graphql/graph/query`,
`internal/api/graphql/graph/materialization`, and the internal dataframe services.
Database access and compiler logic do not belong in `generated/`.

Resolver implementations are the narrow exception to the no-edit rule: gqlgen
creates their package and method signatures, then preserves method bodies and
adjacent adapter helpers across regeneration. Edit transport mapping there when
needed, but do not move application services or storage logic into that package.

The handwritten GraphQL inputs and runtime wiring are deliberately separate:

| Directory | Responsibility |
| --- | --- |
| `internal/api/graphql/graph/schema/` | Handwritten primary GraphQL schema. |
| `internal/api/graphql/graph/` | HTTP handler and shared GraphQL error presentation. |
| `internal/api/graphql/graph/query/` | Arango graph and FHIR dataframe request services. |
| `internal/api/graphql/graph/materialization/` | Published-dataframe transport mapping. |

## Sources of truth and outputs

| Input | Command | Generated outputs |
| --- | --- | --- |
| `schemas/graph-fhir.json` and `cmd/generate/` | `make generate-fhir` | `generated/fhir/*.go`, `generated/fhirschema/generated.go`, and `generated/graphql/graph/schema/fhir_schema.graphqls` |
| `internal/api/graphql/graph/schema/schema.graphqls`, generated FHIR SDL, and `gqlgen.yml` | `make generate-graphql` | Primary executor, models, and resolver bindings under `generated/graphql/graph/` and `internal/api/graphql/graph/resolver/` |

`generated/graphql/graph/executor/fhir_schema.generated.go` and
`generated/graphql/graph/executor/root_.generated.go` are
large because gqlgen emits executable dispatch code for every selectable field
in the typed FHIR schema. They are generated runtime code, not duplicate data
models or a second query engine.

`internal/api/graphql/graph/resolver/fhir_schema.resolvers.go` contains generated
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
