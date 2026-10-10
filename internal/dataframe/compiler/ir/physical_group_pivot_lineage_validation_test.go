package ir

import "testing"

func TestPhysicalCountRowsGroupPivotLineageAcceptsExactTypedOwner(t *testing.T) {
	for _, policy := range []PhysicalStageGroupMissingKeyPolicy{
		PhysicalStageGroupMissingKeyGroup, PhysicalStageGroupMissingKeyExclude, PhysicalStageGroupMissingKeyError,
	} {
		t.Run(string(policy), func(t *testing.T) {
			sequence, terminal, trace, binds, source := validCountRowsGroupPivotLineageFixture()
			sequence.Stages[0].Group.MissingKeyPolicy = policy
			if err := validatePhysicalConstructionPivotLineageTrace(sequence, terminal, trace, source, binds); err != nil {
				t.Fatalf("valid Group/Pivot trace with %q key semantics was rejected: %v", policy, err)
			}
		})
	}
}

func TestPhysicalCountRowsGroupPivotLineageRejectsMismatchedTypedOwners(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalStageSequence, *PhysicalRowLineageReturn, *PhysicalRowLineageTrace, map[string]any)
	}{
		{
			name: "unmapped Group key",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				group := sequence.Stages[0].Group
				group.Keys = append(group.Keys, PhysicalStageGroupKey{InputColumn: "site", OutputColumn: "site_group", Variable: "site_key", Kind: "STRING"})
			},
		},
		{
			name: "non COUNT_ROWS aggregate",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				sequence.Stages[0].Group.Aggregates[0].Operation = "SUM"
			},
		},
		{
			name: "Group row values",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				sequence.Stages[0].Group.RowValues = []PhysicalStageRowValue{{InputColumn: "score", InputKind: "INTEGER", Output: "scores", Policy: "ALL"}}
			},
		},
		{
			name: "Pivot row values",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				sequence.Stages[1].GroupedPivot.RowValues = []PhysicalStageRowValue{{InputColumn: "note", InputKind: "STRING", Output: "notes", Policy: "ALL"}}
			},
		},
		{
			name: "Pivot category not a Group key",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				sequence.Stages[1].GroupedPivot.CategoryColumn = "unmapped_status"
			},
		},
		{
			name: "Pivot key type mismatch",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, _ map[string]any) {
				sequence.Stages[1].GroupedPivot.GroupKeys[0].Kind = "INTEGER"
			},
		},
		{
			name: "forged stage owner",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any) {
				trace.Stages[0].StageID = "another_pivot"
			},
		},
		{
			name: "wrong typed Pivot key bind",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, binds map[string]any) {
				binds["patient_key"] = 7
			},
		},
		{
			name: "missing Pivot key bind",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, binds map[string]any) {
				delete(binds, "patient_key")
				trace.Stages[0].IdentityKeyBindKeys[0] = "missing_patient_key"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, trace, binds, source := validCountRowsGroupPivotLineageFixture()
			test.mutate(&sequence, &terminal, &trace, binds)
			if err := validatePhysicalConstructionPivotLineageTrace(sequence, terminal, trace, source, binds); err == nil {
				t.Fatal("mismatched Group/Pivot lineage owner unexpectedly validated")
			}
		})
	}
}

func validCountRowsGroupPivotLineageFixture() (PhysicalStageSequence, PhysicalRowLineageReturn, PhysicalRowLineageTrace, map[string]any, []PhysicalOperation) {
	sequence := PhysicalStageSequence{
		SourceStageID: "source", SourceRowIdentity: "_key", FinalStageID: "pivot", FinalRowIdentity: "pivot_identity",
		Stages: []PhysicalConstructionStage{
			{
				ID: "group", InputStageID: "source", Kind: PhysicalStageGroupOp, RowIdentityColumn: "group_identity",
				Group: &PhysicalStageGroup{
					MissingKeyPolicy: PhysicalStageGroupMissingKeyGroup,
					Keys: []PhysicalStageGroupKey{
						{InputColumn: "patient_ref", OutputColumn: "patient_group", Variable: "patient_group_key", Kind: "STRING"},
						{InputColumn: "status", OutputColumn: "status_group", Variable: "status_group_key", Kind: "STRING"},
					},
					Aggregates: []PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "records", Variable: "record_count", OutputKind: "integer"}},
				},
			},
			{
				ID: "pivot", InputStageID: "group", Kind: PhysicalStagePivotOp, RowIdentityColumn: "pivot_identity",
				GroupedPivot: &PhysicalGroupedPivot{
					ConstructionID: "lineage_pivot", ConstructionIDBindKey: "construction_id",
					GroupKeys:      []PhysicalGroupedPivotKey{{Column: "patient_group", Kind: "STRING"}},
					CategoryColumn: "status_group", CategoryType: "STRING", ValueColumn: "records", ValueType: "INTEGER",
					Categories:      []PhysicalGroupedPivotCategory{{Output: "active_rows", MatchKind: PhysicalPivotCategoryValueMatch, ValueBindKey: "active", ValueKind: "STRING"}},
					DuplicatePolicy: "ERROR", MissingCellPolicy: "NULL", UnlistedCategoryPolicy: "ERROR",
				},
			},
		},
	}
	terminal := PhysicalRowLineageReturn{
		RowIDBindKey: "row_id", OffsetBindKey: "offset", LimitBindKey: "limit", FetchLimitBindKey: "fetch",
		ResourceType: "Observation", ResourceIDColumn: "resource_id", OccurrenceKeyColumn: "occurrence_key",
	}
	trace := PhysicalRowLineageTrace{Stages: []PhysicalRowLineageStageMatch{{
		StageID: "pivot", Kind: PhysicalStagePivotOp, StageRowIDBindKey: "row_id", IdentityKeyBindKeys: []string{"patient_key"},
	}}}
	binds := map[string]any{
		"collection": "Observation", "construction_id": "lineage_pivot", "row_id": ` ["GROUPED_PIVOT","lineage_pivot",["STRING","Patient/7"]] `,
		"offset": 0, "limit": 10, "fetch": 11, "patient_key": "Patient/7", "active": "active",
	}
	source := []PhysicalOperation{
		{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "collection"}},
		{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{Name: "resource_id", Hidden: true, Value: PhysicalValue{Variable: "root", Path: []string{"payload", "id"}}}}}},
	}
	return sequence, terminal, trace, binds, source
}
