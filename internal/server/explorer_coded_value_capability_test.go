package server

import (
	"reflect"
	"sort"
	"testing"

	"github.com/calypr/loom/internal/catalog"
	"github.com/calypr/loom/internal/explorer/capability"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCapabilityEvidenceSynthesizesCodeableConceptTextFieldFromProfiler(t *testing.T) {
	profiler := catalog.NewProfilerForGenerationWithLimits("project-a", "generation-a", "", "Observation", nil, catalog.ProfileLimits{})
	profiler.ObservePayload(map[string]any{
		"resourceType": "Observation",
		"code": map[string]any{"coding": []any{
			map[string]any{"system": "urn:study", "code": "stage", "display": "Stage"},
		}},
		"valueCodeableConcept": map[string]any{"coding": []any{
			map[string]any{"system": "urn:staging", "code": "stage-1", "display": "Stage I"},
			map[string]any{"system": "urn:staging", "code": "stage-2", "display": "Stage II"},
		}},
	}, map[string]float64{})

	documents := profiler.Documents()
	fieldValues := make([]catalog.FieldEnrichmentObservation, 0, len(documents))
	var profiledObservation *catalog.SemanticObservation
	for _, document := range documents {
		fieldValues = append(fieldValues, catalog.FieldEnrichmentObservation{
			Project: document.Project, DatasetGeneration: document.DatasetGeneration,
			ResourceType: document.ResourceType, Path: document.Path, Kind: document.Kind,
			DocCount: document.DocCount, MaxItems: document.MaxItems, SampleCount: document.SampleCount,
			DistinctValues:       append([]string(nil), document.DistinctValues...),
			SemanticObservations: append([]catalog.SemanticObservation(nil), document.SemanticObservations...),
		})
		for index := range document.SemanticObservations {
			observation := document.SemanticObservations[index]
			if observation.RuleHint == catalog.SemanticRuleHintCodedValueV1 && observation.Key.Code == "stage" {
				profiledObservation = &observation
			}
		}
	}
	if profiledObservation == nil {
		t.Fatal("profiler emitted no coded-value observation for Observation.code")
	}
	if profiledObservation.Value.Selector != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(profiledObservation.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
		!profiledObservation.Value.FallbackOnBlankString {
		t.Fatalf("profiled CodeableConcept value = %#v, want text primary and blank-aware coding display fallback", profiledObservation.Value)
	}
	wantExamples := []string{"Stage I", "Stage II"}
	gotExamples := append([]string(nil), profiledObservation.Examples...)
	sort.Strings(gotExamples)
	if profiledObservation.Status != "SUPPORTED" || !reflect.DeepEqual(gotExamples, wantExamples) {
		t.Fatalf("profiled CodeableConcept status/examples = %q/%v, want SUPPORTED and %v", profiledObservation.Status, gotExamples, wantExamples)
	}

	evidence := capabilityEvidenceFromCatalog(catalog.CapabilityEvidence{
		FieldEnrichment: catalog.FieldEnrichmentResult{Values: fieldValues},
	})
	var primary *capability.FieldObservation
	for index := range evidence.Fields {
		field := &evidence.Fields[index]
		if field.ResourceType == "Observation" && field.Path == "valueCodeableConcept.text" {
			primary = field
			break
		}
	}
	if primary == nil {
		t.Fatalf("catalog bridge did not synthesize the primary scalar candidate; fields = %#v", evidence.Fields)
	}
	metadata, ok := fhirschema.ResolveTerminalScalarMetadata("Observation", primary.Path)
	if !ok || metadata.Primitive != fhirschema.PrimitiveString || primary.LogicalType != "string" {
		t.Fatalf("synthesized field %q is not accepted as a generated string scalar: metadata=%#v ok=%t field=%#v", primary.Path, metadata, ok, primary)
	}
	if len(primary.ConceptCandidates) != 1 {
		t.Fatalf("synthesized scalar concept candidates = %#v, want the profiler observation", primary.ConceptCandidates)
	}
	concept := primary.ConceptCandidates[0]
	gotExamples = append([]string(nil), concept.Examples...)
	sort.Strings(gotExamples)
	if concept.SourceResourceType != "Observation" || concept.SourcePath != "code" ||
		concept.SourceCanonical != "Observation.code" || concept.KeySelector != "code.coding[]" ||
		concept.System != "urn:study" || concept.Code != "stage" || concept.Display != "Stage" ||
		concept.ValueSelector != "valueCodeableConcept.text" || concept.Status != "SUPPORTED" ||
		!reflect.DeepEqual(gotExamples, wantExamples) {
		t.Fatalf("synthesized scalar concept = %#v, want the profiler identity and text value selector with both examples", concept)
	}

	// The bridge exposes the checked text scalar while catalog consumers retain
	// the original blank-aware fallback on their semantic observation.
	var retained *catalog.SemanticObservation
	for _, field := range fieldValues {
		for index := range field.SemanticObservations {
			observation := &field.SemanticObservations[index]
			if observation.RuleHint == catalog.SemanticRuleHintCodedValueV1 && observation.Key.Code == "stage" {
				retained = observation
			}
		}
	}
	if retained == nil || retained.Value.Selector != "valueCodeableConcept.text" ||
		!reflect.DeepEqual(retained.Value.FallbackSelectors, []string{"valueCodeableConcept.coding[].display"}) ||
		!retained.Value.FallbackOnBlankString {
		t.Fatalf("catalog semantic evidence changed across capability conversion: %#v", retained)
	}
}
