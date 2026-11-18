# Code generation

Loom checks generated Go code into Git. Change the source schema or generator,
then regenerate the affected files.

| Source | Command | Output |
| --- | --- | --- |
| Official HL7 R5 assets in `fhir/r5/` and `cmd/generate/` | `make generate-fhir` | FHIR models, validation, edge extraction, and the resource registry in `fhir/model/`. Compiler schema metadata in `fhir/schema/`. |
| `api/openapi.yaml`, `api/oapi-codegen.yaml`, and `api/templates/` | `make generate-openapi` | HTTP request and response models, Fiber adapters, and the embedded API contract in `api/loomapi/`. |

Run `make generate-fhir` offline using the [HL7 R5 assets](../fhir/r5/README.md).
The JSON Schema supplies resource definitions. The StructureDefinition snapshots
supply reference targets and field metadata. The generator emits forward edges
for supported references and checks the selected schema constraints with
generated Go code. Common primitive patterns use direct string checks.
The loader preserves contained resources in the payload. Edge extraction
covers the supported reference paths outside contained resources, extensions,
and recursive datatype tails.

Ingest records the digest of the generation inputs in new publication schema
identities.

Remove `--schema` from ingest and server commands and `server.schema` from server
YAML. New loads use the generated resource registry. Existing published data
retains its recorded schema identity. A schema change requires a full reload
before partial source updates can resume. Re-ingesting data can change which
rows and relationships are available because the official definitions now
control validation and edge extraction.

Run `make generate` for both generators. Commit the generated files alongside
their inputs. The OpenAPI adapters call the handwritten services under
`internal/server` and `internal/api`.
