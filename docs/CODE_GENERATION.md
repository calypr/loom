# Code generation

Loom checks generated Go code into Git. Change the source schema or generator,
then regenerate the affected files.

| Source | Command | Output |
| --- | --- | --- |
| `fhir/graph-fhir.json` and `cmd/generate/` | `make generate-fhir` | FHIR models, validation, edge extraction, and the resource registry in `fhir/model/`. Compiler schema metadata in `fhir/schema/`. |
| `api/openapi.yaml`, `api/oapi-codegen.yaml`, and `api/templates/` | `make generate-openapi` | HTTP request and response models, Fiber adapters, and the embedded API contract in `api/loomapi/`. |

Run `make generate` for both generators. Commit the generated files alongside
their inputs. The OpenAPI adapters call the handwritten services under
`internal/server` and `internal/api`.
