# Official HL7 FHIR R5 assets

Loom uses the HL7 FHIR R5 5.0.0 release. The JSON files here are copies of the
published assets. The list in `resources.txt` controls which concrete resource
types Loom generates loaders for.

| Local input | Source | SHA-256 of the published archive |
| --- | --- | --- |
| `fhir.schema.json` | [FHIR JSON Schema](https://hl7.org/fhir/R5/fhir.schema.json.zip) | `8d8fdffdf1ee0b25951a28a9c8870bc06a0fa340a9a90e6bd21d706473ec7011` |
| `definitions/` | [HL7 FHIR R5 core package](https://packages.fhir.org/hl7.fhir.r5.core/5.0.0) | `74b27cd1bfce9e80eaceac431edf230b0945a443564fbf5512f82e5fa50a80d4` |

The JSON Schema archive contains a single file. The selected StructureDefinition
files and the package manifest come from the core package. The snapshots supply
reference targets and field metadata.

The files are published by HL7. See the [HL7 FHIR R5 license](https://hl7.org/fhir/R5/license.html).
