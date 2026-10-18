package recipe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConstructionGroupMissingKeyPolicyDefaultsAndPersists(t *testing.T) {
	var legacy ConstructionGroup
	if err := json.Unmarshal([]byte(`{"constructionId":"group","keys":[{"inputColumnId":"input","outputColumnId":"output"}]}`), &legacy); err != nil {
		t.Fatalf("decode legacy group without missingKeyPolicy: %v", err)
	}
	if legacy.MissingKeyPolicy != ConstructionGroupMissingKeyGroup {
		t.Fatalf("legacy missingKeyPolicy = %q, want GROUP", legacy.MissingKeyPolicy)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("marshal legacy group: %v", err)
	}
	if !strings.Contains(string(encoded), `"missingKeyPolicy":"GROUP"`) {
		t.Fatalf("legacy group did not persist explicit GROUP policy: %s", encoded)
	}

	input := map[string]StageColumn{"input": {ID: "input"}}
	output := map[string]StageColumn{"output": {ID: "output"}}
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}, false); err != nil {
		t.Fatalf("validate legacy group: %v", err)
	}
	legacy.MissingKeyPolicy = "SILENT_DEFAULT"
	if err := validateConstructionGroup(legacy, input, output, "steps[0]", map[string]bool{}, false); err == nil || !strings.Contains(err.Error(), "missingKeyPolicy") {
		t.Fatalf("unsupported missingKeyPolicy error = %v", err)
	}
}

func TestConstructionAllowsEmptySourceOnlyForCompilerBoundGroupFieldSource(t *testing.T) {
	binding := &ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: ConstructionSourceProjectionID,
		SourceShapeID: "shape-1", SourceRole: "CURRENT_ITEM", OccurrenceID: "observation",
		OwnerResourceType: "Observation", ResourceType: "Coding", CanonicalPath: "valueCodeableConcept.coding[]",
	}
	construction := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "group", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionGroupOp, Group: &ConstructionGroup{
			ConstructionID: "coding-group", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
			Keys: []ConstructionGroupKey{{FieldSource: binding, OutputColumnID: "coding_label"}},
		}},
		Outputs: []StageColumn{{ID: "coding_label", Name: "coding_label", Type: "string"}},
	}}}
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("validate empty-source compiler-bound Group: %v", err)
	}

	unbound := construction
	unbound.Steps = append([]ConstructionStep(nil), construction.Steps...)
	unbound.Steps[0].Operation.Group = &ConstructionGroup{
		ConstructionID: "unbound-group", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
		Keys: []ConstructionGroupKey{{InputColumnID: "missing", OutputColumnID: "coding_label"}},
	}
	if err := unbound.Validate(nil); err == nil || !strings.Contains(err.Error(), "source projection must contain at least one column") {
		t.Fatalf("unbound empty-source Group error = %v", err)
	}
}

func TestConstructionRowValueFieldSourceIsGroupOnlyAndMutuallyExclusive(t *testing.T) {
	base := ConstructionRowValue{
		OutputColumnID: "values", Policy: ConstructionRowValueAll,
		FieldSource: &ConstructionGroupFieldSource{
			Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: ConstructionSourceProjectionID,
			SourceShapeID: "shape-1", SourceRole: "ROOT", OccurrenceID: "base",
			OwnerResourceType: "Observation", ResourceType: "Observation", CandidateID: "note",
			CanonicalPath: "status",
		},
	}
	group := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "group", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionGroupOp, Group: &ConstructionGroup{
			ConstructionID: "group-id", MissingKeyPolicy: ConstructionGroupMissingKeyGroup,
		}},
		Outputs:   []StageColumn{{ID: "values", Name: "values", Type: "array"}},
		RowValues: []ConstructionRowValue{base},
	}}}
	if err := group.Validate(nil); err != nil {
		t.Fatalf("validate bound Group row value with empty source schema: %v", err)
	}

	conflict := group
	conflict.Steps = append([]ConstructionStep(nil), group.Steps...)
	conflict.Steps[0].RowValues = []ConstructionRowValue{{
		InputColumnID: "status", FieldSource: base.FieldSource, OutputColumnID: "values", Policy: ConstructionRowValueAll,
	}}
	if err := conflict.Validate(nil); err == nil || !strings.Contains(err.Error(), "choose exactly one source representation") {
		t.Fatalf("conflicting row value source error = %v", err)
	}
}

func TestConstructionGroupFieldSourceUsesTaggedCurrentOrRelatedInput(t *testing.T) {
	currentJSON := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-current","input":{"kind":"CURRENT_STAGE","currentStage":{"sourceRole":"ROOT","occurrenceId":"base","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"}}}`
	var current ConstructionGroupFieldSource
	if err := json.Unmarshal([]byte(currentJSON), &current); err != nil {
		t.Fatalf("decode tagged current source: %v", err)
	}
	if err := validateConstructionGroupFieldSourceBinding(current, "fieldSource", false); err != nil {
		t.Fatalf("validate tagged current source: %v", err)
	}

	legacyJSON := `{"kind":"BINDING","valueKind":"SCALAR","sourceStageId":"source_projection","sourceShapeId":"shape-current","sourceRole":"ROOT","occurrenceId":"base","ownerResourceType":"Observation","resourceType":"Observation","candidateId":"status","canonicalPath":"status"}`
	var legacy ConstructionGroupFieldSource
	if err := json.Unmarshal([]byte(legacyJSON), &legacy); err != nil {
		t.Fatalf("decode legacy current source: %v", err)
	}
	legacyInput, err := normalizedConstructionGroupFieldSourceInput(legacy, "fieldSource")
	if err != nil || legacyInput.Kind != "CURRENT_STAGE" || legacyInput.CurrentStage == nil {
		t.Fatalf("legacy source normalization = %#v, %v", legacyInput, err)
	}
	if err := validateConstructionGroupFieldSourceBinding(legacy, "fieldSource", false); err != nil {
		t.Fatalf("validate legacy current source: %v", err)
	}

	related := ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "SCALAR", SourceStageID: ConstructionSourceProjectionID, SourceShapeID: "shape-related",
		Input: &ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &ConstructionGroupRelatedOccurrenceFieldSource{
			AnchorOccurrenceID: "observation-a", AnchorNodeID: "node-observation-a", AnchorResourceType: "Observation",
			Route: []ConstructionGroupRelatedRouteStep{{
				StepIndex: 0, FromOccurrenceID: "observation-a", FromNodeID: "node-observation-a", FromResourceType: "Observation",
				EdgeID: "edge-device", Relationship: "device_Reference", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
				ToOccurrenceID: "device-device-a", ToNodeID: "node-device-a", ToResourceType: "Device",
			}},
			TargetOccurrenceID: "device-device-a", TargetNodeID: "node-device-a", TargetResourceType: "Device",
			ResourceType: "Device", CandidateID: "device-status", CanonicalPath: "status",
			EmptyPolicy: ExpansionPreserveParent, Multiplicity: ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
		}},
	}
	if err := validateConstructionGroupFieldSourceBinding(related, "fieldSource", true); err != nil {
		t.Fatalf("validate related occurrence source: %v", err)
	}

	encoded, err := json.Marshal(related)
	if err != nil {
		t.Fatalf("marshal related occurrence source: %v", err)
	}
	var roundTrip ConstructionGroupFieldSource
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("decode related occurrence source: %v", err)
	}
	if err := validateConstructionGroupFieldSourceBinding(roundTrip, "fieldSource", true); err != nil {
		t.Fatalf("validate related occurrence round trip: %v", err)
	}

	badTag := related
	badTag.Input = &ConstructionGroupFieldSourceInput{Kind: "CURRENT_STAGE", RelatedOccurrence: related.Input.RelatedOccurrence}
	if err := validateConstructionGroupFieldSourceBinding(badTag, "fieldSource", true); err == nil {
		t.Fatal("related payload with CURRENT_STAGE discriminator was accepted")
	}
	badRoute := related
	badRoute.Input = &ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: related.Input.RelatedOccurrence}
	badRelated := *related.Input.RelatedOccurrence
	badRoute.Input.RelatedOccurrence = &badRelated
	badRoute.Input.RelatedOccurrence.Route = append([]ConstructionGroupRelatedRouteStep(nil), related.Input.RelatedOccurrence.Route...)
	badRoute.Input.RelatedOccurrence.Route[0].FromOccurrenceID = "another-owner"
	if err := validateConstructionGroupFieldSourceBinding(badRoute, "fieldSource", true); err == nil {
		t.Fatal("discontinuous related route was accepted")
	}
}

func TestConstructionGroupRelatedCodingIdentityRequiresExactSelectorAndPolicies(t *testing.T) {
	source := ConstructionGroupFieldSource{
		Kind: "BINDING", ValueKind: "CODING_IDENTITY", SourceStageID: ConstructionSourceProjectionID, SourceShapeID: "shape-related-coding",
		Input: &ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: &ConstructionGroupRelatedOccurrenceFieldSource{
			AnchorOccurrenceID: "observation-a", AnchorNodeID: "node-observation-a", AnchorResourceType: "Observation",
			Route: []ConstructionGroupRelatedRouteStep{{
				StepIndex: 0, FromOccurrenceID: "observation-a", FromNodeID: "node-observation-a", FromResourceType: "Observation",
				EdgeID: "edge-device", Relationship: "device_Reference", StorageDirection: "OUTBOUND", MatchMode: "OPTIONAL",
				ToOccurrenceID: "device-device-a", ToNodeID: "node-device-a", ToResourceType: "Device",
			}},
			TargetOccurrenceID: "device-device-a", TargetNodeID: "node-device-a", TargetResourceType: "Device",
			ResourceType: "Device", EmptyPolicy: ExpansionPreserveParent,
			Multiplicity:   ConstructionGroupRelatedMultiplicityDistinctTargetPerInput,
			CodingSelector: "udiCarrier.carrierHRF", CodingEmptyPolicy: ExpansionExclude,
			OrdinalSemantics: ConstructionGroupRelatedOrdinalSemanticsZeroBasedPosition,
		}},
	}
	if err := validateConstructionGroupFieldSourceBinding(source, "fieldSource", true); err != nil {
		t.Fatalf("validate related Coding identity: %v", err)
	}
	bad := source
	bad.Input = &ConstructionGroupFieldSourceInput{Kind: "RELATED_OCCURRENCE", RelatedOccurrence: source.Input.RelatedOccurrence}
	badRelated := *source.Input.RelatedOccurrence
	bad.Input.RelatedOccurrence = &badRelated
	bad.Input.RelatedOccurrence.CandidateID = "fake-candidate"
	if err := validateConstructionGroupFieldSourceBinding(bad, "fieldSource", true); err == nil {
		t.Fatal("related Coding identity accepted a candidate ID")
	}
}
