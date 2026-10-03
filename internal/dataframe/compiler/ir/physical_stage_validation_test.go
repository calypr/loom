package ir

import "testing"

func TestRelatedEligibilityAcceptsRetainedRootKeyDistinctFromExpandedRowIdentity(t *testing.T) {
	stage := PhysicalConstructionStage{
		InputRowVariable: "input",
		InputColumns: []PhysicalStageColumn{
			{
				ID: "root_key", Name: "_key", Kind: "string", Cardinality: "required_one",
				Internal: true, RootContributorResourceType: "Patient",
			},
			{
				ID: "expanded_identity", Name: "__loom_expansion_identity", Kind: "string", Cardinality: "required_one",
				Internal: true, Identity: true,
			},
		},
	}
	subplan := PhysicalSubplan{
		Captures: []string{"input"},
		Operations: []PhysicalOperation{
			{
				Kind:           PhysicalCollectionScanOp,
				Source:         PhysicalSource{ResourceType: "Patient"},
				CollectionScan: &PhysicalCollectionScan{Variable: "root", CollectionBindKey: "root_collection"},
			},
			{
				Kind: PhysicalFilterOp,
				Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
					Operator: "EQUALS",
					Left:     PhysicalValue{Variable: "root", Path: []string{"_key"}},
					Right:    &PhysicalValue{Variable: "input", Path: []string{"_key"}},
				}},
			},
		},
	}
	bindVars := map[string]any{"root_collection": "Patient"}
	if err := validateRelatedEligibilityAnchor(stage, &subplan, bindVars); err != nil {
		t.Fatalf("retained root-key anchor with a distinct expanded row identity did not validate: %v", err)
	}

	invalidProofs := []struct {
		name   string
		mutate func(*PhysicalStageColumn)
	}{
		{"wrong name", func(column *PhysicalStageColumn) { column.Name = "_id" }},
		{"wrong kind", func(column *PhysicalStageColumn) { column.Kind = "integer" }},
		{"optional key", func(column *PhysicalStageColumn) { column.Cardinality = "optional_one" }},
		{"wrong resource type", func(column *PhysicalStageColumn) { column.RootContributorResourceType = "Observation" }},
		{"public key", func(column *PhysicalStageColumn) { column.Internal = false }},
		{"related-record identity metadata", func(column *PhysicalStageColumn) {
			column.RelatedRecordAnchor = &PhysicalStageRelatedRecordAnchor{NodeID: "patient", ResourceType: "Patient"}
		}},
	}
	for _, test := range invalidProofs {
		t.Run(test.name, func(t *testing.T) {
			invalidStage := stage
			invalidStage.InputColumns = append([]PhysicalStageColumn(nil), stage.InputColumns...)
			test.mutate(&invalidStage.InputColumns[0])
			if err := validateRelatedEligibilityAnchor(invalidStage, &subplan, bindVars); err == nil {
				t.Fatal("invalid root-key anchor proof unexpectedly validated")
			}
		})
	}
}
