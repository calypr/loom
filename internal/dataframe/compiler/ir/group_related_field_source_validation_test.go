package ir

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPhysicalGroupRelatedCodingSourceRequiresExactRouteAndSelector(t *testing.T) {
	field, adapter := groupRelatedCodingFieldSourceForTest(t)
	if !validPhysicalGroupFieldSource(field, "source_projection") {
		t.Fatal("exact related Coding source did not validate")
	}
	valid := PhysicalStageGroup{RelatedOccurrence: adapter, Keys: []PhysicalStageGroupKey{{FieldSource: field}}}
	stage := PhysicalConstructionStage{InputStageID: "source_projection"}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(valid, stage); err != nil {
		t.Fatalf("exact embedded route and selector did not validate: %v", err)
	}

	changedSelector := clonePhysicalStageGroupFieldSource(field)
	changedSelector.RelatedOccurrence.CodingSelector = "valueQuantity.value"
	invalid := PhysicalStageGroup{RelatedOccurrence: adapter, Keys: []PhysicalStageGroupKey{{FieldSource: changedSelector}}}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(invalid, stage); err == nil {
		t.Fatal("related Coding source accepted a selector different from the typed target projection")
	}

	changedRoute := clonePhysicalStageGroupFieldSource(field)
	changedRoute.RelatedOccurrence.Route[0].EdgeID = "other-edge"
	invalid = PhysicalStageGroup{RelatedOccurrence: adapter, Keys: []PhysicalStageGroupKey{{FieldSource: changedRoute}}}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(invalid, stage); err == nil {
		t.Fatal("related Coding source accepted a route different from its embedded RelatedExpand")
	}

	unsupportedEmptyPolicy := clonePhysicalStageGroupFieldSource(field)
	unsupportedEmptyPolicy.RelatedOccurrence.EmptyPolicy = "SILENT"
	invalid = PhysicalStageGroup{RelatedOccurrence: adapter, Keys: []PhysicalStageGroupKey{{FieldSource: unsupportedEmptyPolicy}}}
	if err := validatePhysicalGroupRelatedOccurrenceAdapter(invalid, stage); err == nil {
		t.Fatal("related Coding source accepted an unsupported route empty policy")
	}
}

func groupRelatedCodingFieldSourceForTest(t *testing.T) (*PhysicalStageGroupFieldSource, *PhysicalStageGroupRelatedOccurrenceAdapter) {
	t.Helper()
	selector, err := spec.ParseSelector("valueCodeableConcept.coding[]")
	if err != nil {
		t.Fatal(err)
	}
	route := []PhysicalStageRelatedRouteStep{{
		StepIndex: 0, FromOccurrenceID: "patient-occ", FromNodeID: "patient-node", FromResourceType: "Patient",
		EdgeID: "subject-edge", Relationship: "subject_Reference", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		ToOccurrenceID: "observation-occ", ToNodeID: "observation-node", ToResourceType: "Observation",
	}}
	groupRoute := []PhysicalStageGroupRelatedRouteStep{{
		StepIndex: 0, FromOccurrenceID: "patient-occ", FromNodeID: "patient-node", FromResourceType: "Patient",
		EdgeID: "subject-edge", Relationship: "subject_Reference", StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
		ToOccurrenceID: "observation-occ", ToNodeID: "observation-node", ToResourceType: "Observation",
	}}
	targetProjection := PhysicalExpression{
		Kind: PhysicalExtractExpression, Cardinality: PhysicalArrayCardinality, NullBehavior: PhysicalEmptyOnNull,
		Extract: &PhysicalExtract{
			Source:       PhysicalValue{Variable: "observation_doc", Path: []string{"payload"}},
			ResourceType: "Observation", Selector: selector, ExecutionMode: PhysicalSelectorDirectScalar,
		},
	}
	relatedRecords := PhysicalStageRelatedExpand{
		RouteID: "related-route-test", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
		AnchorOccurrenceID: "patient-occ", AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
		TargetNodeID: "observation-node", TargetResourceType: "Observation",
		ItemVariable: "related_item", EmptyPolicy: PhysicalUnnestPreserveParent, Route: route,
		RelatedRecords: PhysicalSubplan{
			Unique: true, Sort: &PhysicalValue{Variable: "observation_doc", Path: []string{"_id"}},
			Return: PhysicalExpression{
				Kind: PhysicalObjectExpression, Cardinality: PhysicalObjectCardinality,
				Object: &PhysicalObject{Fields: []PhysicalExpressionProjection{{
					Name: "__loom_group_related_codings", Expression: targetProjection,
				}}},
			},
		},
	}
	unnest := &PhysicalUnnest{
		OutputVariable: "coding_item", Ordinality: "coding_ordinal", EmptyPolicy: PhysicalUnnestExclude,
		Expression: PhysicalExpression{Kind: PhysicalValueExpression, Cardinality: PhysicalArrayCardinality,
			NullBehavior: PhysicalEmptyOnNull, Value: &PhysicalValue{Variable: "related_item", Path: []string{"__loom_group_related_codings"}}},
	}
	adapter := &PhysicalStageGroupRelatedOccurrenceAdapter{
		RouteID: "related-route-test", RelatedRecords: relatedRecords,
		CodingSelector: "valueCodeableConcept.coding[]", CodingArrayColumn: "__loom_group_related_codings",
		CodingEmptyPolicy: PhysicalUnnestExclude, OrdinalSemantics: "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY",
		CodingUnnest: unnest, CodingMembershipColumn: "membership",
	}
	field := &PhysicalStageGroupFieldSource{
		Kind: PhysicalStageGroupFieldSourceRelatedOccurrence,
		RelatedOccurrence: &PhysicalStageGroupRelatedOccurrenceFieldSource{
			RouteID: "related-route-test", SourceStageID: "source_projection", SourceShapeID: "shape-related-coding",
			AnchorOccurrenceID: "patient-occ", AnchorNodeID: "patient-node", AnchorResourceType: "Patient",
			Route: groupRoute, TargetOccurrenceID: "observation-occ", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			ResourceType: "Coding", EmptyPolicy: "PRESERVE_PARENT", Multiplicity: "DISTINCT_TARGET_PER_INPUT",
			CodingSelector: "valueCodeableConcept.coding[]", CodingEmptyPolicy: "EXCLUDE",
			OrdinalSemantics: "ZERO_BASED_POSITION_IN_FLATTENED_EXPANSION_ARRAY", MembershipColumn: "membership",
		},
	}
	return field, adapter
}
