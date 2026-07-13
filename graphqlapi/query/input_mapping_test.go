package queryapi

import (
	"testing"

	"github.com/calypr/loom/graphqlapi/model"
)

func TestBuilderFromInputMapsAggregatesSlicesAndFallbacks(t *testing.T) {
	valueMode := model.FhirValueModeAuto
	op := model.FhirAggregateOperationDistinctValues
	contains := model.FhirFieldPredicateOperationContains
	input := model.FhirDataframeInput{
		Project:          "P1",
		RootResourceType: "Patient",
		RootFields: []*model.FhirFieldSelectInput{
			{
				Name: "case_id",
				Selector: &model.FhirFieldSelectorInput{
					SourcePath: strPtr("identifier[]"),
					Where:      &model.FhirFieldPredicateInput{Path: "system", Op: contains, Value: "case_id"},
					ValuePath:  "value",
				},
				FallbackSelectors: []*model.FhirFieldSelectorInput{
					{
						SourcePath: strPtr("identifier[]"),
						Where:      &model.FhirFieldPredicateInput{Path: "system", Op: contains, Value: "submitter_id"},
						ValuePath:  "value",
					},
				},
				ValueMode: valueMode,
			},
		},
		Traverse: []*model.FhirTraversalStepInput{
			{
				EdgeLabel:      "subject_Patient",
				ToResourceType: "Specimen",
				Alias:          "specimen",
				Aggregates: []*model.FhirAggregateInput{
					{Name: "specimen_types", Operation: op, FhirPath: strPtr("type.coding[].display"), ValueMode: valueMode},
				},
				Slices: []*model.FhirRepresentativeSliceInput{
					{
						Name:        "sample_slice",
						Limit:       2,
						WherePath:   strPtr("type.coding[].display"),
						WhereEquals: strPtr("Primary Tumor"),
						Fields: []*model.FhirFieldSelectInput{
							{Name: "id", Selector: &model.FhirFieldSelectorInput{ValuePath: "id"}, ValueMode: valueMode},
						},
					},
				},
			},
		},
	}

	builder := BuilderFromInput(input)
	if len(builder.Fields) != 1 || len(builder.Fields[0].FallbackSelects) != 1 {
		t.Fatalf("unexpected root field mapping: %#v", builder.Fields)
	}
	if len(builder.Traversals) != 1 {
		t.Fatalf("unexpected traversal mapping: %#v", builder.Traversals)
	}
	step := builder.Traversals[0]
	if len(step.Aggregates) != 1 || step.Aggregates[0].Operation != "DISTINCT_VALUES" {
		t.Fatalf("unexpected aggregate mapping: %#v", step.Aggregates)
	}
	if len(step.Slices) != 1 || step.Slices[0].PredicatePath != "type.coding[].display" || len(step.Slices[0].Fields) != 1 {
		t.Fatalf("unexpected slice mapping: %#v", step.Slices)
	}
}

func strPtr(in string) *string {
	return &in
}
