# Code generation

Loom checks generated code into Git so a normal build does not need a code
generator. Generated files are implementation artifacts: change their inputs,
then regenerate them. Do not edit them by hand.

## Why generated code is not in one directory

Go packages are directory-scoped. The generated code must live beside the
package that compiles and uses it:

| Directory | Package | Why it lives there |
| --- | --- | --- |
| `fhirstructs/` | `fhirstructs` | Typed FHIR payload models imported by the server and gqlgen. |
| `fhirschema/` | `fhirschema` | Generated schema metadata used by the compiler. |
| `graphqlapi/` | `graphqlapi` | gqlgen's executable schema and resolvers must share the server's GraphQL package. |
| `graphqlapi/model/` | `graphqlapi/model` | gqlgen's separate GraphQL input/output model package. |

Putting these in a single `generated/` directory would create different Go
import paths. It would require handwritten adapters between FHIR structs,
compiler metadata, and gqlgen—the duplication generation is meant to avoid.
The directory split is therefore a Go package boundary, not two competing
FHIR implementations.

## Sources of truth and outputs

| Input | Command | Generated outputs |
| --- | --- | --- |
| `schemas/graph-fhir.json` and `cmd/generate/` | `make generate-fhir` | `fhirstructs/{model,validate,extract,helpers,resources,graphql}.go`, `fhirschema/generated.go`, and `graphqlapi/fhir_schema.graphqls` |
| `graphqlapi/schema.graphqls`, `graphqlapi/fhir_schema.graphqls`, and `gqlgen.yml` | `make generate-graphql` | `graphqlapi/*.generated.go`, `graphqlapi/fhir_schema.resolvers.go`, and `graphqlapi/model/models.go` |
| `graphqlapi/clickhouse/schema.graphqls` and `graphqlapi/clickhouse/gqlgen.yml` | `make generate-graphql` | `graphqlapi/clickhouse/generated.go` and its resolver/model bindings |

`graphqlapi/fhir_schema.generated.go` and `graphqlapi/root_.generated.go` are
large because gqlgen emits executable dispatch code for every selectable field
in the typed FHIR schema. They are generated runtime code, not duplicate data
models or a second query engine.

`graphqlapi/fhir_schema.resolvers.go` is also generated, but deliberately
contains thin resolver methods that call shared helpers. It is the bridge that
lets all JSON FHIR property names remain selectable without hand-writing one
resolver per field.

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

To understand a generated result, start at its input above. Do not patch the
output as a shortcut; the next generation will overwrite it.
