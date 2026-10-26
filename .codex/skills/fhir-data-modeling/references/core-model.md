# FHIR core model reference

This file records the FHIR rules that change Loom's semantic decisions. Check the linked R5 definition when a resource profile narrows one of these rules.

## Reusable datatypes

| Datatype | Structural identity | Associated value | Loom rule |
|---|---|---|---|
| `Identifier` | One item's `system` namespace. `type` and `use` add context. | The same item's `value`. | Match and project within one Identifier item. A present `system` and `value` need no human mapping. An absent system weakens cross-dataset identity but does not break the local pair. |
| `Coding` | `system`, optional `version`, and `code` from one Coding item. | `display` is a system-defined label. `userSelected` records selection provenance. | Never use `display` as the code. Never combine a system from one Coding item with a code from another. |
| `CodeableConcept` | Zero or more Coding translations plus optional `text`. | The concept is the whole object. Coding order has no meaning. | Preserve all codings. Select by a requested system or terminology policy. Use `text` as a human fallback, not as an equivalent code. |
| `Quantity` | The containing field supplies the measured concept. `system` and `code` identify the unit. | `value`, `comparator`, `unit`, `system`, and `code` belong together. | Keep the comparator. Treat `unit` as display text. Normalize only a recognized coded unit, normally UCUM. |
| `Range` | One Range object. | `low` and `high` are separate Quantities. | Preserve both bounds and their units. Do not choose one bound as the value without an explicit feature definition. |
| `Ratio` | One Ratio object. | `numerator` and `denominator` are separate Quantities. | Preserve both sides. Do not treat a clinical pair such as blood pressure as a Ratio unless the instance uses this datatype. |
| `Period` | One Period object. | Optional `start` and `end` bounds. | Preserve open bounds. A missing end does not mean the start value is the whole period. |
| `Reference` | The containing resource path and one of `reference`, `identifier`, or `display`. | The target resource, when resolvable. | Build an edge from the containing path. Prefer a literal reference when both literal and logical forms exist. Do not infer reverse or transitive edges as FHIR facts. |
| `CodeableReference` | Either `concept`, `reference`, or both as allowed by the profile. | A class-level concept or an instance-level resource. | Preserve which arm supplied the information. Do not flatten the concept and reference into one opaque string. |
| `Extension` | The absolute root `url` and every nested relative URL in order. | One terminal `value[x]`, or nested extensions instead of a value. | Keep ancestor URL boundaries. The URL path and concrete value arm define the structural pair. A reference-valued extension is normally an edge, not a scalar feature. |

The normative definitions are in [datatypes](https://hl7.org/fhir/R5/datatypes.html), [references](https://hl7.org/fhir/R5/references.html), and [extensibility](https://hl7.org/fhir/R5/extensibility.html).

## Observation

`Observation.code` describes the question, measurement, or assertion. `Observation.value[x]` records the answer. The concrete arm determines the value model.

Each `Observation.component` is its own owner. Pair `component.code` only with that component's `value[x]`. Do not pair one component's code with a neighboring component's value.

An Observation may use components instead of a root value. A missing root `value[x]` does not make that Observation incomplete. `dataAbsentReason` carries the explicit missing-value reason where the resource permits it.

For `valueCodeableConcept`, keep the result concept separate from `Observation.code`. The first identifies the result category. The second identifies what was observed. A result Coding's `display` cannot replace its `code` for computation.

The resource rules and examples are in [FHIR R5 Observation](https://hl7.org/fhir/R5/observation.html).

## JSON and cardinality

FHIR JSON uses a property name with the concrete type suffix for a choice element. For example, `value[x]` becomes `valueQuantity`, `valueString`, or `valueCodeableConcept`.

Profiles may narrow an element from many values to one value, but the JSON representation remains an array when the base definition is repeating. Use the base structure for parsing and the profile for validation.

Primitive extensions appear in a sibling property whose name starts with `_`. Do not discard that sibling when the primitive value is absent because the extension may carry the missing semantic.

See [FHIR R5 JSON](https://hl7.org/fhir/R5/json.html) and [FHIR R5 profiling](https://hl7.org/fhir/R5/profiling.html).

## Terminology and profiles

A code system defines concepts and their codes. A value set selects codes for a use. A concept map relates concepts between systems. These artifacts answer different questions.

Binding strength affects conformance. `required` restricts values to the value set. `extensible` requires a value-set code when one applies. `preferred` and `example` do not make out-of-set values invalid.

Profiles may restrict cardinality, choice arms, target types, terminology bindings, and repeated slices. Slicing uses a discriminator such as a fixed URL, code, or type. Consult the instance's `meta.profile` and the applicable `StructureDefinition` before calling a repeated element ambiguous.

The base structure still carries usable meaning without a profile. A missing profile does not erase Identifier, Coding, Quantity, Reference, or Extension containment rules.

See [FHIR R5 terminology](https://hl7.org/fhir/R5/terminologies.html) and [FHIR R5 profiling](https://hl7.org/fhir/R5/profiling.html).
