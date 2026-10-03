package recipe

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

func TestConstructionValidatesTypedSourceChildIdentity(t *testing.T) {
	child := lineage.SourceChild{
		Kind: lineage.IndexedValueChild, ParentColumnIDs: []string{"source"}, OccurrenceID: "base",
		SourcePath: "name[]", Coordinates: []lineage.Coordinate{{BoundaryPath: "name[]", Index: 1}},
	}
	id, child, err := lineage.StableSourceChildID(child)
	if err != nil {
		t.Fatal(err)
	}
	construction := Construction{Version: 1, SourceColumns: []StageColumn{{ID: id, Name: "name_1", Type: "string", SourceChild: &child}}}
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("valid child lineage: %v", err)
	}

	construction.SourceColumns[0].SourceChild = nil
	if err := construction.Validate(nil); err == nil {
		t.Fatal("generated child ID without typed lineage was accepted")
	}
	construction.SourceColumns[0].SourceChild = &child
	construction.SourceColumns[0].SourceChild.Coordinates[0].Index++
	if err := construction.Validate(nil); err == nil {
		t.Fatal("child ID mismatched with typed lineage was accepted")
	}
}

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

func TestConstructionValidatesEffectiveCohortBoundary(t *testing.T) {
	selected := "a"
	label := "pair"
	construction := Construction{Version: 1, Steps: []ConstructionStep{
		{
			ID: "source_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionFilterOp, Filter: &ConstructionFilter{
				ColumnID: "id", Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &selected}},
			}},
			Outputs: []StageColumn{{ID: "id", Name: "id", Type: "string"}},
		},
		{
			ID: "group_filter", Inputs: []ConstructionInputRef{{Kind: ConstructionStepOutputInput, StepID: ConstructionCohortGroupStageID}},
			Operation: ConstructionOperation{Kind: ConstructionFilterOp, Filter: &ConstructionFilter{
				ColumnID: "group_id", Operator: FilterEquals, Values: []FilterValue{{Kind: FilterString, String: &label}},
			}},
			Outputs: []StageColumn{
				{ID: "group_id", Name: "group_id", Type: "string"},
				{ID: "group_label", Name: "group_label", Type: "string"},
				{ID: "group_ordinal", Name: "group_ordinal", Type: "integer"},
				{ID: "members", Name: "members", Type: "array"},
				{ID: "id", Name: "id", Type: "array"},
			},
		},
	}}
	fields := []Field{{ColumnID: "id", Name: "id", Expr: Expression{Select: "root.id"}}}
	rows := &GroupRows{
		AfterStepID: "source_filter",
		RowValues:   []GroupRowValuePolicy{{ColumnID: "id", Policy: ConstructionRowValueAll}},
	}
	if err := construction.ValidateWithGroupRows(fields, rows); err != nil {
		t.Fatalf("effective source-filter → cohort → group-filter sequence rejected: %v", err)
	}

	construction.Steps[1].Inputs[0].StepID = "source_filter"
	if err := construction.ValidateWithGroupRows(fields, rows); err == nil {
		t.Fatal("post-cohort step referencing the anchor output instead of its immediate virtual predecessor was accepted")
	}
}
