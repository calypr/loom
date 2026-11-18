# Code generation

Loom checks generated Go code into Git. Change the source schema or generator,
then regenerate the affected files.

| Source | Command | Output |
| --- | --- | --- |
| HL7 FHIR R5 StructureDefinitions, `fhir/edge-policy.json`, and `cmd/generate/` | `make generate-fhir` | FHIR models, validation, edge extraction, and the resource registry in `fhir/model/`. Compiler schema metadata in `fhir/schema/`. |
| `api/openapi.yaml`, `api/oapi-codegen.yaml`, and `api/templates/` | `make generate-openapi` | HTTP request and response models, Fiber adapters, and the embedded API contract in `api/loomapi/`. |

`make generate-fhir` downloads `hl7.fhir.r5.core` version 5.0.0 and verifies its
SHA-256 before reading the StructureDefinition snapshots. It reuses the
verified archive under `fhir/.cache/` on later runs. Set `FHIR_PACKAGE_PATH` to
use a local copy of the pinned archive.

Loom's edge policy supplies the stored labels and reverse edges used by
dataframe traversals. Ingestion uses `fhir/graph-fhir.json` by default for the
resource allowlist and publication schema identity.

Run `make generate` for both generators. Commit the generated files alongside
their inputs. The OpenAPI adapters call the handwritten services under
`internal/server` and `internal/api`.
