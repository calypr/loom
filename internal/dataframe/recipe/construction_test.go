package recipe

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/lineage"
)

func TestConstructionAllowsRootRelatedExpandFromIdentityOnlySource(t *testing.T) {
	construction := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "expand_observations", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionRelatedExpandOp, RelatedExpand: &ConstructionRelatedExpand{
			AnchorColumnID: "_key", ChoiceID: "patient-observation", TargetNodeID: "observation-node", TargetResourceType: "Observation",
			Route: []ConstructionRelatedRouteStep{{
				EdgeID: "patient-observation", FromNodeID: "patient-node", ToNodeID: "observation-node",
				FromResourceType: "Patient", ToResourceType: "Observation", Relationship: "subject_Patient",
				StorageDirection: "INBOUND", MatchMode: "OPTIONAL",
			}},
			ContributorPolicy: "ALL_MATCHES", EmptyPolicy: ExpansionPreserveParent, RelatedRecordColumnID: "observation_id",
		}},
		Outputs: []StageColumn{{ID: "observation_id", Name: "observation_id", Type: "string", Nullable: true}},
	}}}
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("root RELATED_EXPAND should accept an identity-only source: %v", err)
	}

	fieldDependent := Construction{Version: 1, Steps: []ConstructionStep{{
		ID: "filter_source", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionFilterOp, Filter: &ConstructionFilter{
			ColumnID: "source_id", Operator: FilterExists,
		}},
		Outputs: []StageColumn{{ID: "source_id", Name: "source_id", Type: "string"}},
	}}}
	if err := fieldDependent.Validate(nil); err == nil || !strings.Contains(err.Error(), "source projection must contain at least one column") {
		t.Fatalf("field-dependent operation should still reject an empty source projection, got %v", err)
	}
}

func TestConstructionPivotUnlistedPolicyContract(t *testing.T) {
	category := "a"
	construction := Construction{Version: 1, Steps: []ConstructionStep{{
		ID:     "pivot",
		Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &ConstructionPivot{
			ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
			CategoryColumnID: "category", ValueColumnID: "value",
			Categories:      []ConstructionPivotCategory{{Key: TableScalar{Kind: TableScalarString, String: &category}, OutputColumnID: "amount_a"}},
			DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
			UnlistedCategoryPolicy: PivotUnlistedCategoryError,
		}},
		Outputs: []StageColumn{
			{ID: "group", Name: "group", Label: "Group", Type: "string"},
			{ID: "amount_a", Name: "amount_a", Label: "Amount A", Type: "string"},
		},
	}}}
	source := []Field{
		{ColumnID: "group", Name: "group", Label: "Group"},
		{ColumnID: "category", Name: "category", Label: "Category"},
		{ColumnID: "value", Name: "value", Label: "Value"},
	}
	if err := construction.Validate(source); err != nil {
		t.Fatalf("ERROR pivot policy rejected: %v", err)
	}
	construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy = PivotUnlistedCategoryExcludeWithEvidence
	if err := construction.Validate(source); err != nil {
		t.Fatalf("EXCLUDE_WITH_EVIDENCE pivot policy rejected: %v", err)
	}
	construction.Steps[0].Operation.Pivot.UnlistedCategoryPolicy = "UNKNOWN"
	if err := construction.Validate(source); err == nil || !strings.Contains(err.Error(), "policies are incomplete or unsupported") {
		t.Fatalf("unsupported construction pivot policy validation = %v", err)
	}
}

func TestConstructionRejectsDuplicatePivotCategoryKeysByIdentity(t *testing.T) {
	category := "a"
	construction := Construction{Version: 1, Steps: []ConstructionStep{{
		ID:     "pivot",
		Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &ConstructionPivot{
			ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
			CategoryColumnID: "category", ValueColumnID: "value",
			Categories: []ConstructionPivotCategory{
				{Key: TableScalar{Kind: TableScalarString, String: &category}, OutputColumnID: "amount_a"},
				{Key: TableScalar{Kind: TableScalarString, String: &category}, OutputColumnID: "amount_b"},
			},
			DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
			UnlistedCategoryPolicy: PivotUnlistedCategoryError,
		}},
		Outputs: []StageColumn{
			{ID: "group", Name: "group", Label: "Group", Type: "string"},
			{ID: "amount_a", Name: "amount_a", Label: "Amount A", Type: "string"},
			{ID: "amount_b", Name: "amount_b", Label: "Amount B", Type: "string"},
		},
	}}}
	source := []Field{
		{ColumnID: "group", Name: "group", Label: "Group"},
		{ColumnID: "category", Name: "category", Label: "Category"},
		{ColumnID: "value", Name: "value", Label: "Value"},
	}
	if err := construction.Validate(source); err == nil || !strings.Contains(err.Error(), "pivot category keys must be unique") {
		t.Fatalf("duplicate scalar category keys validation = %v", err)
	}

	integer := int64(1)
	construction.Steps[0].Operation.Pivot.Categories[1].Key = TableScalar{Kind: TableScalarInteger, Integer: &integer}
	if err := construction.Validate(source); err != nil {
		t.Fatalf("distinct typed category identities rejected: %v", err)
	}
}

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

func pivotConstructionForTypes(groupType, categoryType, valueType string, duplicatePolicy PivotDuplicatePolicy) Construction {
	category := "a"
	return Construction{Version: 1, SourceColumns: []StageColumn{
		{ID: "group", Name: "group", Label: "Group", Type: groupType},
		{ID: "category", Name: "category", Label: "Category", Type: categoryType},
		{ID: "value", Name: "value", Label: "Value", Type: valueType},
	}, Steps: []ConstructionStep{{
		ID:     "pivot",
		Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
		Operation: ConstructionOperation{Kind: ConstructionPivotOp, Pivot: &ConstructionPivot{
			ConstructionID: "pivot", GroupKeyIDs: []string{"group"},
			CategoryColumnID: "category", ValueColumnID: "value",
			Categories:      []ConstructionPivotCategory{{Key: TableScalar{Kind: TableScalarString, String: &category}, OutputColumnID: "amount_a"}},
			DuplicatePolicy: duplicatePolicy, MissingCellPolicy: PivotMissingCellNull,
			UnlistedCategoryPolicy: PivotUnlistedCategoryError,
		}},
		Outputs: []StageColumn{
			{ID: "group", Name: "group", Label: "Group", Type: groupType},
			{ID: "amount_a", Name: "amount_a", Label: "Amount A", Type: valueType},
		},
	}}}
}

func TestConstructionPivotRejectsNonScalarOperands(t *testing.T) {
	tests := []struct {
		name        string
		groupType   string
		categoryTyp string
		valueType   string
		want        string
	}{
		{name: "group key array", groupType: "array", categoryTyp: "string", valueType: "decimal", want: `pivot group key "group" type "array" is not a scalar`},
		{name: "category object", groupType: "string", categoryTyp: "object", valueType: "decimal", want: `pivot categoryColumnId column "category" type "object" is not a scalar`},
		{name: "value array", groupType: "string", categoryTyp: "string", valueType: "array", want: `pivot valueColumnId column "value" type "array" is not a scalar`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			construction := pivotConstructionForTypes(test.groupType, test.categoryTyp, test.valueType, PivotDuplicateError)
			err := construction.Validate(nil)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("non-scalar pivot operand error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestConstructionPivotRejectsCategoryKeyKindMismatch(t *testing.T) {
	construction := pivotConstructionForTypes("string", "integer", "decimal", PivotDuplicateError)
	err := construction.Validate(nil)
	if err == nil || !strings.Contains(err.Error(), `does not match category column type "integer"`) {
		t.Fatalf("category key kind mismatch error = %v", err)
	}
}

func TestConstructionPivotSumRequiresNumericValueColumn(t *testing.T) {
	construction := pivotConstructionForTypes("string", "string", "string", PivotDuplicateSum)
	err := construction.Validate(nil)
	if err == nil || !strings.Contains(err.Error(), `pivot duplicatePolicy SUM requires an INTEGER or DECIMAL value column, got "string"`) {
		t.Fatalf("SUM string value error = %v", err)
	}
}

func TestConstructionPivotSumAcceptsNumericValueColumns(t *testing.T) {
	for _, logicalType := range []string{"integer", "decimal"} {
		construction := pivotConstructionForTypes("string", "string", logicalType, PivotDuplicateSum)
		if err := construction.Validate(nil); err != nil {
			t.Errorf("SUM %s value column rejected: %v", logicalType, err)
		}
	}
}

func TestConstructionPivotSkipsTypeChecksWithoutSchemaTypes(t *testing.T) {
	construction := pivotConstructionForTypes("", "", "", PivotDuplicateSum)
	if err := construction.Validate(nil); err != nil {
		t.Fatalf("pivot with unresolved types rejected: %v", err)
	}
}

func TestConstructionCodedPivotLogicalTypeGate(t *testing.T) {
	construction := func(logicalType string) Construction {
		return Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "pivot", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionCodedPivotOp, CodedPivot: &ConstructionCodedPivot{
				ConstructionID: "pivot",
				Source: ConstructionCodedPivotSource{
					BindingID: "binding", ResourceType: "Observation", SourcePath: "Observation.value",
					KeyPath: "code.coding[]", ValuePath: "valueString", LogicalType: logicalType,
					RuleVersion: "1", SchemaVersion: 1, CandidateID: "candidate", NodeID: "node", FieldPath: "Observation.value",
				},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "value", OutputColumnID: "value_column"}},
				DuplicatePolicy: PivotDuplicateError, MissingCellPolicy: PivotMissingCellNull,
			}},
			Outputs: []StageColumn{{ID: "value_column", Name: "value_column", Label: "Value", Type: "string", Nullable: true}},
		}}}
	}

	for _, logicalType := range []string{"string", "date_time", "date-time", "datetime", "number", "decimal", "integer", "boolean", "code", "uuid", "date"} {
		if err := construction(logicalType).Validate(nil); err != nil {
			t.Errorf("coded pivot source logical type %q rejected: %v", logicalType, err)
		}
	}
	for _, logicalType := range []string{"money", "object", "quantity"} {
		if err := construction(logicalType).Validate(nil); err == nil || !strings.Contains(err.Error(), "not a supported table scalar") {
			t.Errorf("coded pivot source logical type %q error = %v, want unsupported table scalar", logicalType, err)
		}
	}
}

func TestConstructionCodedPivotSumRequiresNumericSourceLogicalType(t *testing.T) {
	withPolicy := func(logicalType, outputType string, policy PivotDuplicatePolicy) Construction {
		construction := Construction{Version: 1, Steps: []ConstructionStep{{
			ID: "pivot", Inputs: []ConstructionInputRef{{Kind: ConstructionSourceProjectionInput}},
			Operation: ConstructionOperation{Kind: ConstructionCodedPivotOp, CodedPivot: &ConstructionCodedPivot{
				ConstructionID: "pivot",
				Source: ConstructionCodedPivotSource{
					BindingID: "binding", ResourceType: "Observation", SourcePath: "Observation.value",
					KeyPath: "code.coding[]", ValuePath: "valueString", LogicalType: logicalType,
					RuleVersion: "1", SchemaVersion: 1, CandidateID: "candidate", NodeID: "node", FieldPath: "Observation.value",
				},
				Categories:      []ConstructionCodedPivotCategory{{System: "urn:example", Code: "value", OutputColumnID: "value_column"}},
				DuplicatePolicy: policy, MissingCellPolicy: PivotMissingCellNull,
			}},
			Outputs: []StageColumn{{ID: "value_column", Name: "value_column", Label: "Value", Type: outputType, Nullable: true}},
		}}}
		return construction
	}

	err := withPolicy("string", "string", PivotDuplicateSum).Validate(nil)
	if err == nil || !strings.Contains(err.Error(), "SUM requires an INTEGER or DECIMAL source logicalType") {
		t.Fatalf("coded pivot SUM with string source error = %v, want numeric source rejection", err)
	}
	for _, logicalType := range []string{"integer", "decimal", "number"} {
		if err := withPolicy(logicalType, "decimal", PivotDuplicateSum).Validate(nil); err != nil {
			t.Errorf("coded pivot SUM with %s source rejected: %v", logicalType, err)
		}
	}
}
