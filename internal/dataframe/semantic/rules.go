package semantic

import (
	"strings"

	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

type primitiveRule struct{}

func (primitiveRule) ID() string      { return RulePrimitiveRepeated }
func (primitiveRule) Precedence() int { return 100 }
func (primitiveRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Shape == "primitive" || source.Shape == "array" && source.ItemReference == ""
}
func (primitiveRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	valueType := source.Primitive
	if valueType == "" || valueType == string(fhirschema.PrimitiveUnknown) {
		valueType = "string"
	}
	cardinality := CardinalityOptionalOne
	if source.Repeated {
		cardinality = CardinalityRepeated
	}
	return []Concept{{Label: labelFor(source, ""), Group: "Common", Description: "Observed primitive data", Output: OutputDescriptor{Mode: OutputScalar, ValueType: valueType, Cardinality: cardinality, Selection: Selection{Mode: OutputScalar, SourcePath: source.SourcePath, ValueSelector: source.ValuePath}}}}
}

type complexRule struct{}

func (complexRule) ID() string      { return RuleComplexDatatype }
func (complexRule) Precedence() int { return 200 }
func (complexRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Shape == "object" || source.ItemReference != ""
}
func (complexRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	return []Concept{{Label: labelFor(source, "Details"), Group: "Other", Description: "Structured FHIR data; choose a more specific field when available", Output: OutputDescriptor{Mode: OutputMetadata, ValueType: "object", Cardinality: cardinality(source), Generic: true, Selection: Selection{Mode: OutputMetadata, SourcePath: source.Path}}}}
}

type identifierRule struct{}

func (identifierRule) ID() string      { return "identifier" }
func (identifierRule) Precedence() int { return 300 }
func (identifierRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "Identifier" || source.ItemReference == "Identifier"
}
func (identifierRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	path := source.Path
	if source.ItemReference == "Identifier" {
		path = strings.TrimSuffix(path, "[]") + "[]"
	}
	return []Concept{{Label: labelFor(source, "Value"), Group: "Identifiers", Description: "Identifier value keyed by its issuing system", Output: OutputDescriptor{Mode: OutputDynamicFamily, ValueType: "string", Cardinality: CardinalityRepeated, Selection: Selection{Mode: OutputDynamicFamily, ItemSource: path, KeySelector: "system", ValueSelector: "value"}}}}
}

type codingRule struct{}

func (codingRule) ID() string      { return "coding" }
func (codingRule) Precedence() int { return 300 }
func (codingRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "Coding" || source.ItemReference == "Coding"
}
func (codingRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	path := source.Path
	if source.ItemReference == "Coding" {
		path = strings.TrimSuffix(path, "[]") + "[]"
	}
	return []Concept{{Label: labelFor(source, "Display"), Group: "Coded values", Description: "Human-readable coding display, falling back to code", Output: OutputDescriptor{Mode: OutputCodedValue, ValueType: "code", Cardinality: CardinalityRepeated, Selection: Selection{Mode: OutputCodedValue, ItemSource: path, KeySelector: "display", ValueSelector: "code", ValueFallbacks: []string{"display"}}}}}
}

type codeableConceptRule struct{}

func (codeableConceptRule) ID() string      { return "codeable_concept" }
func (codeableConceptRule) Precedence() int { return 300 }
func (codeableConceptRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "CodeableConcept" || source.ItemReference == "CodeableConcept"
}
func (codeableConceptRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	path := source.Path
	itemSource := ""
	if source.ItemReference == "CodeableConcept" {
		itemSource = strings.TrimSuffix(path, "[]") + "[]"
	}
	return []Concept{{Label: labelFor(source, "Coded value"), Group: "Coded values", Description: "CodeableConcept text or coding display/code", Output: OutputDescriptor{Mode: OutputCodedValue, ValueType: "code", Cardinality: cardinality(source), Selection: Selection{Mode: OutputCodedValue, ItemSource: itemSource, SourcePath: path, KeySelector: "coding[].display", ValueSelector: "text", ValueFallbacks: []string{"coding[].display", "coding[].code"}}}}}
}

type quantityRule struct{}

func (quantityRule) ID() string      { return "quantity" }
func (quantityRule) Precedence() int { return 300 }
func (quantityRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "Quantity" || source.ItemReference == "Quantity"
}
func (quantityRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	return []Concept{{Label: labelFor(source, "Value"), Group: "Measurements", Description: "Numeric quantity with its unit", Output: OutputDescriptor{Mode: OutputMeasurement, ValueType: "decimal", Cardinality: cardinality(source), Selection: Selection{Mode: OutputMeasurement, SourcePath: source.Path, ValueSelector: "value", ValueFallbacks: []string{"unit"}}}}}
}

type referenceRule struct{}

func (referenceRule) ID() string      { return "reference" }
func (referenceRule) Precedence() int { return 300 }
func (referenceRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "Reference" || source.ItemReference == "Reference"
}
func (referenceRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	return []Concept{{Label: labelFor(source, "Reference"), Group: "Relationships", Description: "FHIR reference target", Output: OutputDescriptor{Mode: OutputReference, ValueType: "reference", Cardinality: cardinality(source), Selection: Selection{Mode: OutputReference, SourcePath: source.Path, ValueSelector: "reference", ValueFallbacks: []string{"display", "identifier.value"}}}}}
}

type extensionRule struct{}

func (extensionRule) ID() string      { return "extension" }
func (extensionRule) Precedence() int { return 360 }
func (extensionRule) Match(_ SchemaDescriptor, source SourceDescriptor) bool {
	return source.Reference == "Extension" || source.ItemReference == "Extension"
}
func (extensionRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	path := source.Path
	if source.ItemReference == "Extension" {
		path = strings.TrimSuffix(path, "[]") + "[]"
	}
	return []Concept{{Label: labelFor(source, "Value"), Group: "Extensions", Description: "Extension URL keyed by its value[x] member", Output: OutputDescriptor{Mode: OutputDynamicFamily, ValueType: "mixed", Cardinality: CardinalityRepeated, Selection: Selection{Mode: OutputDynamicFamily, ItemSource: path, KeySelector: "url", ValueSelector: "value[x]", Transforms: []string{"last_segment", "sanitize_name"}}}}}
}

type choiceRule struct{}

func (choiceRule) ID() string      { return "choice" }
func (choiceRule) Precedence() int { return 400 }
func (choiceRule) Match(schema SchemaDescriptor, source SourceDescriptor) bool {
	if source.Path == "" || !strings.Contains(source.Path, "value") {
		return false
	}
	return schema.ResourceType == "Observation" || source.Reference == "Extension"
}
func (choiceRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	valueSelector := choiceValueSelector(source)
	valueType := source.Primitive
	if valueType == "" || valueType == string(fhirschema.PrimitiveUnknown) {
		switch source.Reference {
		case "Quantity":
			valueType = "decimal"
		case "CodeableConcept":
			valueType = "code"
		default:
			valueType = "mixed"
		}
	}
	return []Concept{{Label: labelFor(source, "Value"), Group: "Measurements", Description: "FHIR choice value[x]", Output: OutputDescriptor{Mode: OutputMeasurement, ValueType: valueType, Cardinality: cardinality(source), Selection: Selection{Mode: OutputMeasurement, SourcePath: "", ValueSelector: valueSelector}}}}
}

func choiceValueSelector(source SourceDescriptor) string {
	if source.Shape == "object" {
		switch source.Reference {
		case "Quantity":
			return source.Path + ".value"
		case "CodeableConcept":
			return source.Path + ".text"
		case "Period":
			return source.Path + ".start"
		case "Range":
			return source.Path + ".low.value"
		case "Ratio":
			return source.Path + ".numerator.value"
		}
	}
	return source.Path
}

type observationRule struct{}

func (observationRule) ID() string      { return "observation" }
func (observationRule) Precedence() int { return 400 }
func (observationRule) Match(schema SchemaDescriptor, source SourceDescriptor) bool {
	if schema.ResourceType != "Observation" {
		return false
	}
	return source.Path == "code" || source.ItemReference == "ObservationComponent" || source.Reference == "ObservationComponent"
}
func (observationRule) Render(schema SchemaDescriptor, source SourceDescriptor) []Concept {
	itemSource := ""
	if source.Path == "code" {
		itemSource = ""
	}
	if source.ItemReference == "ObservationComponent" || source.Reference == "ObservationComponent" {
		itemSource = strings.TrimSuffix(source.Path, "[]") + "[]"
	}
	valueSelector := firstObservationValue(schema, itemSource)
	if valueSelector == "" {
		valueSelector = "value[x]"
	}
	return []Concept{{Label: labelFor(source, "Measurement"), Group: "Measurements", Description: "Observation code paired with its value", Output: OutputDescriptor{Mode: OutputMeasurement, ValueType: "mixed", Cardinality: CardinalityPivoted, Selection: Selection{Mode: OutputMeasurement, ItemSource: itemSource, SourcePath: source.Path, KeySelector: "code.coding[].display", ValueSelector: valueSelector, ValueFallbacks: []string{"code.coding[].code", "code.text"}}}}}
}

func firstObservationValue(schema SchemaDescriptor, itemSource string) string {
	prefix := strings.TrimSuffix(itemSource, ".")
	choices := []string{"valueQuantity.value", "valueCodeableConcept.text", "valueCodeableConcept.coding[].display", "valueString", "valueInteger", "valueBoolean", "valueDecimal", "valueDateTime", "valueTime", "valuePeriod.start", "valueRange.low.value", "valueRatio.numerator.value"}
	for _, choice := range choices {
		path := choice
		if prefix != "" {
			path = prefix + "." + choice
		}
		for _, field := range schema.Fields {
			if field.Path == path || field.Path == choice {
				return choice
			}
		}
	}
	return ""
}

func cardinality(source SourceDescriptor) string {
	if source.Repeated || source.Shape == "array" || source.ItemReference != "" {
		return CardinalityRepeated
	}
	return CardinalityOptionalOne
}

func genericConcept(source SourceDescriptor, label, description, ruleID string, precedence int) Concept {
	if strings.TrimSpace(label) == "" {
		label = labelFor(source, "")
	}
	concept := Concept{Label: label, Group: "Other", Description: description, RuleID: ruleID, Precedence: precedence, Source: source.canonical(), Output: OutputDescriptor{Mode: OutputMetadata, ValueType: source.Primitive, Cardinality: cardinality(source), Generic: true, Selection: Selection{Mode: OutputMetadata, SourcePath: source.Path}}, Trace: traceFor(Concept{}, source, ruleID, precedence, true), Examples: source.safeExamples(5)}
	concept.ID = conceptID(concept)
	return concept
}

func traceFor(_ Concept, source SourceDescriptor, ruleID string, precedence int, fallback bool) TraceDescriptor {
	return TraceDescriptor{ResourceType: source.ResourceType, RawPath: source.Path, SourcePath: source.SourcePath, ValuePath: source.ValuePath, Reference: source.Reference, RuleID: ruleID, Precedence: precedence, Fallback: fallback}
}

func fhirHasResource(resourceType string) bool { return fhirschema.HasResource(resourceType) }
func fhirFields(resourceType string) []fhirschema.FieldSpec {
	return fhirschema.FieldsForResource(resourceType)
}

func pathPrefixes(path string) []string {
	parts := strings.Split(path, ".")
	out := make([]string, 0, len(parts))
	for i := 1; i <= len(parts); i++ {
		out = append(out, strings.Join(parts[:i], "."))
	}
	return out
}

func describePath(resourceType, path string) SourceDescriptor {
	semantics, ok := fhirschema.ResolveFieldSemantics(resourceType, path)
	selector := fhirschema.FieldSelectorSpecFromPath(path)
	source := SourceDescriptor{ResourceType: resourceType, Path: path, SourcePath: selector.SourcePath, ValuePath: selector.ValuePath, Shape: "primitive", Reference: semantics.Reference, Repeated: strings.Contains(path, "[]")}
	if !ok {
		return source
	}
	switch semantics.Kind {
	case fhirschema.FieldKindObject:
		source.Shape = "object"
	case fhirschema.FieldKindArray:
		source.Shape = "array"
		source.ItemReference = semantics.Reference
		if semantics.ElementKind == fhirschema.FieldKindObject {
			source.Shape = "array"
		}
	}
	metadata, metadataOK := fhirschema.ResolveTerminalScalarMetadata(resourceType, path)
	if metadataOK {
		source.Primitive = string(metadata.Primitive)
		source.Repeated = metadata.Repeated
	}
	return source
}
