# Code generation

Loom checks generated Go code into Git. Change the source schema or generator,
then regenerate the affected files.

| Source | Command | Output |
| --- | --- | --- |
| `schemas/graph-fhir.json` and `cmd/generate/` | `make generate-fhir` | FHIR models, validation, edge extraction, and the resource registry in `generated/fhir/`. Compiler schema metadata in `generated/fhirschema/`. |
| `openapi/openapi.yaml`, `openapi/oapi-codegen.yaml`, and `openapi/templates/` | `make generate-openapi` | HTTP request and response models, Fiber adapters, and the embedded API contract in `generated/loomapi/`. |

Run `make generate` for both generators. Commit the generated files alongside
their inputs. The OpenAPI adapters call the handwritten services under
`internal/server` and `internal/api`.
