package semantic

import (
	"testing"
)

func TestDescribeResourceUsesGeneratedMetadata(t *testing.T) {
	descriptor, err := DescribeResource("Observation")
	if err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Fields) == 0 {
		t.Fatal("expected generated Observation sources")
	}
	var code, component, valueQuantity SourceDescriptor
	for _, source := range descriptor.Fields {
		switch source.Path {
		case "code":
			code = source
		case "component[]":
			component = source
		case "valueQuantity":
			valueQuantity = source
		}
	}
	if code.Reference != "CodeableConcept" || code.Shape != "object" {
		t.Fatalf("code source = %+v", code)
	}
	if component.ItemReference != "ObservationComponent" || component.Shape != "array" {
		t.Fatalf("component source = %+v", component)
	}
	if valueQuantity.Reference != "Quantity" || valueQuantity.Shape != "object" {
		t.Fatalf("valueQuantity source = %+v", valueQuantity)
	}
}

func TestDefaultRegistryRendersResearcherConceptsAndTrace(t *testing.T) {
	descriptor, err := DescribeResource("Observation")
	if err != nil {
		t.Fatal(err)
	}
	result := DefaultRegistry().Discover(descriptor)
	if len(result.Concepts) == 0 || len(result.Families) == 0 {
		t.Fatalf("empty semantic result: %+v", result)
	}
	concept := conceptWithPath(result.Concepts, "code")
	if concept == nil {
		t.Fatal("missing Observation code concept")
	}
	if concept.RuleID != "observation" || concept.Output.Mode != OutputMeasurement {
		t.Fatalf("code concept = %+v", *concept)
	}
	valueConcept := conceptWithPath(result.Concepts, "valueQuantity")
	if valueConcept == nil || valueConcept.RuleID != "choice" || valueConcept.Output.ValueType != "decimal" || valueConcept.Output.Selection.ValueSelector != "valueQuantity.value" {
		t.Fatalf("choice concept = %+v", valueConcept)
	}
	if concept.Trace.RawPath != "code" || concept.Trace.RuleID != "observation" || concept.Trace.Fallback {
		t.Fatalf("code trace = %+v", concept.Trace)
	}
	if concept.ID == "" || concept.ID != conceptID(*concept) {
		t.Fatalf("unstable concept ID: %+v", concept)
	}
}

func TestInitialRulesCoverFHIRFamilies(t *testing.T) {
	tests := []struct {
		resource string
		path     string
		rule     string
		mode     string
	}{
		{"Patient", "identifier[]", "identifier", OutputDynamicFamily},
		{"Condition", "code", "codeable_concept", OutputCodedValue},
		{"Observation", "code", "observation", OutputMeasurement},
		{"Observation", "valueQuantity", "choice", OutputMeasurement},
		{"DocumentReference", "content[].attachment.extension[]", "extension", OutputDynamicFamily},
		{"ResearchSubject", "study", "reference", OutputReference},
	}
	for _, tc := range tests {
		descriptor, err := DescribeResource(tc.resource)
		if err != nil {
			t.Fatalf("%s: %v", tc.resource, err)
		}
		result := DefaultRegistry().Discover(descriptor)
		concept := conceptWithPath(result.Concepts, tc.path)
		if concept == nil {
			t.Fatalf("%s missing concept for %s", tc.resource, tc.path)
		}
		if concept.RuleID != tc.rule || concept.Output.Mode != tc.mode {
			t.Fatalf("%s/%s got rule=%q mode=%q, want %q/%q", tc.resource, tc.path, concept.RuleID, concept.Output.Mode, tc.rule, tc.mode)
		}
	}
}

func TestProfilePrecedenceUnknownRuleAndSafeExamples(t *testing.T) {
	descriptor := SchemaDescriptor{ResourceType: "Patient", Fields: []SourceDescriptor{{ResourceType: "Patient", Path: "id", Shape: "primitive", Primitive: "string", Examples: []Example{{Value: "secret-id", Safe: false}, SafeExample("female")}}}, Profiles: []ProfileBinding{{Path: "id", RuleID: "profile.custom", Label: "Participant identifier"}}}
	result := DefaultRegistry().Discover(descriptor)
	concept := conceptWithPath(result.Concepts, "id")
	if concept == nil || !concept.Output.Generic || concept.Label != "Participant identifier" {
		t.Fatalf("profile fallback = %+v", concept)
	}
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "unknown_rule" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	if len(concept.Examples.Values) != 1 || concept.Examples.Values[0] != "female" {
		t.Fatalf("unsafe example was not filtered: %+v", concept.Examples)
	}
}

func TestRegistryReportsSamePrecedenceAmbiguity(t *testing.T) {
	source := SourceDescriptor{ResourceType: "Patient", Path: "id", Shape: "primitive", Primitive: "string"}
	registry := NewRegistry(testRule{id: "a", precedence: 250}, testRule{id: "b", precedence: 250})
	result := registry.Discover(SchemaDescriptor{ResourceType: "Patient", Fields: []SourceDescriptor{source}})
	if len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "ambiguous_rules" {
		t.Fatalf("diagnostics = %+v", result.Diagnostics)
	}
	concept := conceptWithPath(result.Concepts, "id")
	if concept == nil || !concept.Output.Generic || !concept.Trace.Fallback {
		t.Fatalf("ambiguity concept = %+v", concept)
	}
}

func TestConceptAndFamilyIDsAreDeterministic(t *testing.T) {
	source := SourceDescriptor{ResourceType: "Patient", Path: "id", Shape: "primitive", Primitive: "string"}
	a := DefaultRegistry().Discover(SchemaDescriptor{ResourceType: "Patient", Fields: []SourceDescriptor{source}})
	b := DefaultRegistry().Discover(SchemaDescriptor{ResourceType: "Patient", Fields: []SourceDescriptor{source}})
	if len(a.Concepts) != len(b.Concepts) || a.Concepts[0].ID != b.Concepts[0].ID || a.Families[0].ID != b.Families[0].ID {
		t.Fatalf("IDs changed: a=%+v b=%+v", a, b)
	}
}

type testRule struct {
	id         string
	precedence int
}

func (r testRule) ID() string                                        { return r.id }
func (r testRule) Precedence() int                                   { return r.precedence }
func (r testRule) Match(_ SchemaDescriptor, _ SourceDescriptor) bool { return true }
func (r testRule) Render(_ SchemaDescriptor, source SourceDescriptor) []Concept {
	return []Concept{{Label: "Test", Output: OutputDescriptor{Mode: OutputScalar, Selection: Selection{SourcePath: source.Path}}}}
}

func conceptWithPath(concepts []Concept, path string) *Concept {
	for i := range concepts {
		if concepts[i].Source.Path == path {
			return &concepts[i]
		}
	}
	return nil
}
