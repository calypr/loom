---
name: fhir-data-modeling
description: Model or review FHIR JSON semantics in Loom, including datatype correlations, choice values, terminology, profiles, extensions, references, semantic catalog entries, and ML dataframe columns. Use when code interprets a FHIR structure rather than copying an already selected scalar field.
---

# FHIR data modeling

Use the FHIR specification and the generated schema as the source of truth. Do not infer a FHIR relationship from neighboring JSON keys or from a field-name suffix alone.

## Establish the contract

1. Identify the FHIR release from the dataset schema and the generated model. Loom's current generated model and the open CDA-FHIR corpus use R5 structures, including `CodeableReference`.
2. Read the relevant release-specific datatype or resource definition and its examples.
3. Resolve the path through `internal/fhir/schema`. Use generated cardinality, item type, choice arms, and terminal primitive metadata.
4. Inspect representative records from `/Users/peterkor/Desktop/BMEG/loom/CDA-FHIR/META`. Treat the corpus as usage evidence, not as a replacement for the specification.
5. State what owns the value before writing code. Preserve the owning repeated item, Observation component, extension ancestry, or reference path.

Read [core-model.md](references/core-model.md) before changing semantic extraction, authoring, compilation, or product labels. Read [cda-fhir-corpus.md](references/cda-fhir-corpus.md) when the change targets the aggregator data or claims that a pattern is common, absent, or complete.

## Classify the evidence

Keep these states separate in types, API responses, tests, and UI copy:

- `STRUCTURAL`: the base FHIR datatype defines the association. Examples include one Identifier item's `system` and `value`, one Coding item's `system` and `code`, and one Extension item's `url` and `value[x]`.
- `PROFILED`: a `StructureDefinition` or implementation guide fixes a choice, slice, terminology binding, or cardinality needed by the interpretation.
- `TERMINOLOGY`: a code system, value set, or concept map supplies meaning beyond the JSON structure.
- `DATA_QUALITY_WARNING`: the shape is readable, but the instance conflicts with a FHIR invariant, terminology identity, unit rule, or dataset plausibility check.
- `UNSUPPORTED`: FHIR defines the relationship, but Loom cannot compile or present it yet.
- `AMBIGUOUS`: the available FHIR structure, profile, and terminology evidence leave two or more materially different interpretations.

Never label `UNSUPPORTED` as `AMBIGUOUS` or "Needs mapping." A human should resolve only a real semantic choice. Missing Loom code is an engineering task.

## Build dataframe features

- Keep the structural key and value in the same lexical owner. Never zip independently flattened arrays.
- Preserve every repeated boundary until the user chooses a reduction such as `FIRST`, `ALL`, `DISTINCT`, a keyed lookup, or an aggregate.
- Preserve the concrete `value[x]` arm. Do not coalesce different arms into one scalar without an explicit typed policy.
- Keep a coded identity as `system`, optional `version`, and `code`. Treat `display` as presentation text. Do not use `display` as a computational identity.
- Keep a Quantity's numeric value, comparator, display unit, unit system, and unit code available together. Normalize only coded units with a declared conversion.
- Treat a Reference as an edge owned by its containing resource path. FHIR does not imply reverse or transitive relationships.
- Emit provenance for an ML column. Include the resource type, structural path, selected key, value arm, reduction, unit policy, and any quality warning.

## Verify the interpretation

Add a fixture that would fail if values cross repeated owners or if a choice arm is silently lost. Check the compiled dataframe value, its metadata, and the user-facing status. For aggregator behavior, run the relevant project verification skill against the real open fixture.

Use official sources:

- [FHIR R5 datatypes](https://hl7.org/fhir/R5/datatypes.html)
- [FHIR R5 datatype examples](https://hl7.org/fhir/R5/datatypes-examples.html)
- [FHIR R5 JSON representation](https://hl7.org/fhir/R5/json.html)
- [FHIR R5 references](https://hl7.org/fhir/R5/references.html)
- [FHIR R5 extensibility](https://hl7.org/fhir/R5/extensibility.html)
- [FHIR R5 profiling](https://hl7.org/fhir/R5/profiling.html)
- [FHIR R5 terminology](https://hl7.org/fhir/R5/terminologies.html)
- [FHIR R5 Observation](https://hl7.org/fhir/R5/observation.html)
