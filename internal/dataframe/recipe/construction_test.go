package recipe

import "testing"

func TestConstructionValidatesTypedIntermediateSequence(t *testing.T) {
	stringScalar := func(value string) TableScalar {
		return TableScalar{Kind: TableScalarString, String: &value}
	}
	columns := func(values ...StageColumn) []StageColumn { return values }
	group := StageColumn{ID: "col_group", Name: "group", Type: "string"}
	amount := StageColumn{ID: "col_amount", Name: "amount", Type: "decimal"}
	count := StageColumn{ID: "col_count", Name: "count", Type: "decimal"}
	derived := StageColumn{ID: "col_total", Name: "total", Type: "decimal"}
	key := StageColumn{ID: "col_key", Name: "measure", Type: "string"}
	value := StageColumn{ID: "col_value", Name: "measure_value", Type: "decimal"}
	integerTen := int64(10)
	construction := Construction{Version: 1, Steps: []ConstructionStep{
		{
			ID: "step_pivot", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &ConstructionPivot{
				ConstructionID: "pivot_amounts", GroupKeyIDs: []string{"col_group"},
				CategoryColumnID: "col_category", ValueColumnID: "col_value_source",
				Categories: []ConstructionPivotCategory{
					{Key: stringScalar("amount"), OutputColumnID: "col_amount"},
					{Key: stringScalar("count"), OutputColumnID: "col_count"},
				},
				DuplicatePolicy: PivotDuplicateSum, MissingCellPolicy: PivotMissingCellNull,
				UnlistedCategoryPolicy: PivotUnlistedCategoryError,
			}},
			Outputs: columns(group, amount, count),
		},
		{
			ID: "step_derive", Inputs: []ConstructionInputRef{{Kind: ConstructionStepOutputInput, StepID: "step_pivot"}},
			Operation: ConstructionOperation{Kind: ConstructionDeriveOp, Derive: &ConstructionDerive{
				ConstructionID: "derive_total", OutputColumnID: derived.ID, Operation: DerivedAdd,
				Left:               ConstructionOperand{Kind: DerivedColumnOperand, ColumnID: amount.ID},
				Right:              ConstructionOperand{Kind: DerivedColumnOperand, ColumnID: count.ID},
				MissingInputPolicy: MissingInputPropagateNull,
			}},
			Outputs: columns(group, amount, count, derived),
		},
		{
			ID: "step_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionStepOutputInput, StepID: "step_derive"}},
			Operation: ConstructionOperation{Kind: ConstructionFilterOp, Filter: &ConstructionFilter{
				ColumnID: derived.ID, Operator: FilterGreaterEq,
				Values: []FilterValue{{Kind: FilterInteger, Integer: &integerTen}},
			}},
			Outputs: columns(group, amount, count, derived),
		},
		{
			ID: "step_unpivot", Inputs: []ConstructionInputRef{{Kind: ConstructionStepOutputInput, StepID: "step_filter"}},
			Operation: ConstructionOperation{Kind: ConstructionUnpivotOp, Unpivot: &ConstructionUnpivot{
				ConstructionID: "unpivot_amounts", Inputs: []ConstructionUnpivotInput{
					{ColumnID: amount.ID, Key: stringScalar("amount")},
					{ColumnID: count.ID, Key: stringScalar("count")},
				},
				KeyOutputColumnID: key.ID, ValueOutputColumnID: value.ID, NullRowPolicy: UnpivotNullPreserve,
			}},
			Outputs: columns(group, derived, key, value),
		},
	}}

	source := []Field{
		{ColumnID: "col_group", Name: "group", Label: "Group"},
		{ColumnID: "col_category", Name: "category", Label: "Category"},
		{ColumnID: "col_value_source", Name: "value", Label: "Value"},
	}
	if err := construction.Validate(source); err != nil {
		t.Fatalf("valid pivot→derive→filter→unpivot sequence rejected: %v", err)
	}

	construction.Steps[2].Inputs[0].StepID = "step_pivot"
	if err := construction.Validate(source); err == nil {
		t.Fatal("filter stage referencing a non-immediate output was accepted")
	}
}
