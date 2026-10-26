package capability_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/explorer/authoringv2"
	"github.com/calypr/loom/internal/explorer/capability"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

func TestSemanticFallbackDTOJSONRoundTripAndLegacyDefaults(t *testing.T) {
	wantFallbacks := []string{"valueCodeableConcept.coding[].display", "valueString"}
	wantBlankFallback := true

	t.Run("semantic binding choice source", func(t *testing.T) {
		value := capability.SemanticBindingChoiceSource{
			Kind:                  capability.ConstructionChoiceSourceKind("SEMANTIC"),
			ConceptID:             "concept-id",
			BindingID:             "binding-id",
			CandidateID:           "candidate-id",
			NodeID:                "node-id",
			ResourceType:          "Observation",
			SourcePath:            "code",
			FieldPath:             "valueCodeableConcept.text",
			ValueSelector:         "valueCodeableConcept.text",
			ValueFallbacks:        append([]string(nil), wantFallbacks...),
			FallbackOnBlankString: wantBlankFallback,
			LogicalType:           "string",
			RuleHint:              "CODED_VALUE_V1",
			RuleVersion:           "5",
			SchemaVersion:         1,
			Cardinality:           "ONE",
		}
		got := roundTripJSON(t, value, &capability.SemanticBindingChoiceSource{}).(capability.SemanticBindingChoiceSource)
		assertFallbackFields(t, got.ValueFallbacks, got.FallbackOnBlankString, wantFallbacks, wantBlankFallback)

		var legacy capability.SemanticBindingChoiceSource
		if err := json.Unmarshal([]byte(`{"kind":"SEMANTIC"}`), &legacy); err != nil {
			t.Fatal(err)
		}
		assertFallbackFields(t, legacy.ValueFallbacks, legacy.FallbackOnBlankString, nil, false)
	})

	t.Run("semantic frame family", func(t *testing.T) {
		value := capability.SemanticFrameFamily{
			BindingID:             "binding-id",
			ResourceType:          "Observation",
			SourcePath:            "component[]",
			OwningScope:           "component[]",
			KeyPath:               "code.coding[]",
			ValuePath:             "valueCodeableConcept.text",
			ValueFallbacks:        append([]string(nil), wantFallbacks...),
			FallbackOnBlankString: wantBlankFallback,
			LogicalType:           "string",
			RuleVersion:           "5",
			SchemaVersion:         1,
		}
		got := roundTripJSON(t, value, &capability.SemanticFrameFamily{}).(capability.SemanticFrameFamily)
		assertFallbackFields(t, got.ValueFallbacks, got.FallbackOnBlankString, wantFallbacks, wantBlankFallback)

		var legacy capability.SemanticFrameFamily
		if err := json.Unmarshal([]byte(`{"bindingId":"binding-id"}`), &legacy); err != nil {
			t.Fatal(err)
		}
		assertFallbackFields(t, legacy.ValueFallbacks, legacy.FallbackOnBlankString, nil, false)
	})

	t.Run("generated public source and family DTOs", func(t *testing.T) {
		valueFallbacks := append([]string(nil), wantFallbacks...)
		fallbackOnBlank := wantBlankFallback
		source := loomapi.SemanticBindingChoiceSource{
			BindingId: "binding-id", CandidateId: "candidate-id", Cardinality: "ONE", ConceptId: "concept-id",
			FieldPath: "valueCodeableConcept.text", Kind: loomapi.SemanticBindingChoiceSourceKind("SEMANTIC"),
			LogicalType: "string", NodeId: "node-id", ResourceType: "Observation", RuleVersion: "5",
			SchemaVersion: 1, SourcePath: "code", ValueSelector: "valueCodeableConcept.text",
			ValueFallbacks: &valueFallbacks, FallbackOnBlankString: &fallbackOnBlank,
		}
		gotSource := roundTripJSON(t, source, &loomapi.SemanticBindingChoiceSource{}).(loomapi.SemanticBindingChoiceSource)
		if gotSource.ValueFallbacks == nil || gotSource.FallbackOnBlankString == nil {
			t.Fatalf("generated semantic source omitted fallback fields: %#v", gotSource)
		}
		assertFallbackFields(t, *gotSource.ValueFallbacks, *gotSource.FallbackOnBlankString, wantFallbacks, wantBlankFallback)

		family := loomapi.SemanticFrameFamily{
			BindingId: "binding-id", ResourceType: "Observation", SourcePath: "component[]", OwningScope: "component[]",
			KeyPath: "code.coding[]", ValuePath: "valueCodeableConcept.text", LogicalType: "string",
			RuleVersion: "5", SchemaVersion: 1, ValueFallbacks: &valueFallbacks, FallbackOnBlankString: &fallbackOnBlank,
		}
		gotFamily := roundTripJSON(t, family, &loomapi.SemanticFrameFamily{}).(loomapi.SemanticFrameFamily)
		if gotFamily.ValueFallbacks == nil || gotFamily.FallbackOnBlankString == nil {
			t.Fatalf("generated semantic family omitted fallback fields: %#v", gotFamily)
		}
		assertFallbackFields(t, *gotFamily.ValueFallbacks, *gotFamily.FallbackOnBlankString, wantFallbacks, wantBlankFallback)

		var legacySource loomapi.SemanticBindingChoiceSource
		if err := json.Unmarshal([]byte(`{"kind":"SEMANTIC"}`), &legacySource); err != nil {
			t.Fatal(err)
		}
		if legacySource.ValueFallbacks != nil || legacySource.FallbackOnBlankString != nil {
			t.Fatalf("legacy generated source defaults = %#v, want nil fallback fields", legacySource)
		}
		var legacyFamily loomapi.SemanticFrameFamily
		if err := json.Unmarshal([]byte(`{"bindingId":"binding-id"}`), &legacyFamily); err != nil {
			t.Fatal(err)
		}
		if legacyFamily.ValueFallbacks != nil || legacyFamily.FallbackOnBlankString != nil {
			t.Fatalf("legacy generated family defaults = %#v, want nil fallback fields", legacyFamily)
		}
	})
}

func TestSemanticFrameFallbackSurvivesProposalRequestAndResponseDTOs(t *testing.T) {
	wantFallbacks := []string{"valueCodeableConcept.coding[].display", "valueString"}
	construction := authoringv2.Construction{
		Version: 1,
		Steps: []authoringv2.ConstructionStep{{
			ID:     "pivot-step",
			Inputs: []authoringv2.ConstructionInputRef{{Kind: authoringv2.ConstructionInputSourceProjection}},
			Operation: authoringv2.ConstructionOperation{
				Kind: authoringv2.ConstructionOperationCodedPivot,
				CodedPivot: &authoringv2.ConstructionCodedPivot{
					ConstructionID: "pivot-construction",
					Source: &authoringv2.ConstructionCodedPivotSource{
						Family: capability.SemanticFrameFamily{
							BindingID: "binding-id", ResourceType: "Observation", SourcePath: "component[]",
							SourceCanonical: "Observation.component.code", OwningScope: "component[]",
							KeyPath: "code.coding[]", ValuePath: "valueCodeableConcept.text",
							ValueFallbacks: append([]string(nil), wantFallbacks...), FallbackOnBlankString: true,
							ChoiceArms: []string{"valueCodeableConcept"}, LogicalType: "string", RuleVersion: "5", SchemaVersion: 1,
						},
						CandidateID: "observation-candidate", NodeID: "observation-node",
						FieldPath: "valueCodeableConcept.text",
					},
					Categories:        []authoringv2.ConstructionCodedPivotCategory{{System: "urn:staging", Code: "stage-1", OutputColumnID: "stage_one"}},
					DuplicatePolicy:   authoringv2.ConstructionPivotDuplicateError,
					MissingCellPolicy: authoringv2.ConstructionPivotMissingError,
				},
			},
			Outputs: []authoringv2.StageColumn{},
		}},
	}

	constructionJSON := mustMarshalJSON(t, construction)
	var generatedConstruction loomapi.Construction
	mustUnmarshalJSON(t, constructionJSON, &generatedConstruction)

	request := loomapi.ConstructionProposalRequest{
		CandidateConstruction: generatedConstruction,
		ExpectedDraftDigest:   "draft-digest",
		ExpectedDraftVersion:  1,
		OutputId:              "observations",
		SnapshotToken:         "snapshot-token",
	}
	requestJSON := mustMarshalJSON(t, request)
	var decodedRequest loomapi.ConstructionProposalRequest
	mustUnmarshalJSON(t, requestJSON, &decodedRequest)
	var requestAuthoringConstruction authoringv2.Construction
	mustUnmarshalJSON(t, mustMarshalJSON(t, decodedRequest.CandidateConstruction), &requestAuthoringConstruction)
	assertConstructionFallback(t, requestAuthoringConstruction, wantFallbacks)

	authoringResponse := lifecycle.ConstructionProposalResponse{
		OutputID: "observations", SnapshotToken: "snapshot-token", CandidateConstruction: construction,
	}
	responseJSON := mustMarshalJSON(t, authoringResponse)
	var generatedResponse loomapi.ConstructionProposalResponse
	mustUnmarshalJSON(t, responseJSON, &generatedResponse)
	responseWireJSON := mustMarshalJSON(t, generatedResponse)
	var decodedAuthoringResponse lifecycle.ConstructionProposalResponse
	mustUnmarshalJSON(t, responseWireJSON, &decodedAuthoringResponse)
	assertConstructionFallback(t, decodedAuthoringResponse.CandidateConstruction, wantFallbacks)
}

func assertConstructionFallback(t *testing.T, construction authoringv2.Construction, wantFallbacks []string) {
	t.Helper()
	if len(construction.Steps) != 1 || construction.Steps[0].Operation.CodedPivot == nil || construction.Steps[0].Operation.CodedPivot.Source == nil {
		t.Fatalf("construction lost coded pivot source across API boundary: %#v", construction)
	}
	family := construction.Steps[0].Operation.CodedPivot.Source.Family
	assertFallbackFields(t, family.ValueFallbacks, family.FallbackOnBlankString, wantFallbacks, true)
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func mustUnmarshalJSON(t *testing.T, encoded []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(encoded, destination); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func roundTripJSON(t *testing.T, value any, destination any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, destination); err != nil {
		t.Fatalf("decode JSON round trip: %v", err)
	}
	switch result := destination.(type) {
	case *capability.SemanticBindingChoiceSource:
		return *result
	case *capability.SemanticFrameFamily:
		return *result
	case *loomapi.SemanticBindingChoiceSource:
		return *result
	case *loomapi.SemanticFrameFamily:
		return *result
	default:
		t.Fatalf("unexpected destination type %T", destination)
		return nil
	}
}

func assertFallbackFields(t *testing.T, got []string, gotBlank bool, want []string, wantBlank bool) {
	t.Helper()
	if !reflect.DeepEqual(got, want) || gotBlank != wantBlank {
		t.Fatalf("fallback fields = %#v/%t, want %#v/%t", got, gotBlank, want, wantBlank)
	}
}
