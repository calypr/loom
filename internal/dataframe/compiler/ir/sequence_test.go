package ir

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestPhysicalRelatedRowLineageTraceAcceptsScopedOrderedOwners(t *testing.T) {
	sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
	if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err != nil {
		t.Fatalf("valid ordered trace was rejected: %v", err)
	}
}

func TestPhysicalRelatedRowLineageTraceRejectsInvalidOwnerWitnesses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalStageSequence, *PhysicalRowLineageReturn, *PhysicalRowLineageTrace, map[string]any, []PhysicalOperation)
	}{
		{
			name: "out of order owners",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages[0], trace.Stages[1] = trace.Stages[1], trace.Stages[0]
			},
		},
		{
			name: "missing owner",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages = trace.Stages[:1]
			},
		},
		{
			name: "repeated owner",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, _ map[string]any, _ []PhysicalOperation) {
				trace.Stages[1] = trace.Stages[0]
			},
		},
		{
			name: "forged terminal resource path",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ *PhysicalRowLineageTrace, bindVars map[string]any, _ []PhysicalOperation) {
				bindVars["terminal_0"] = "Encounter/forged"
			},
		},
		{
			name: "empty identity without preserve policy",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, trace *PhysicalRowLineageTrace, bindVars map[string]any, _ []PhysicalOperation) {
				trace.Stages[0].RelatedRowKind = "EMPTY"
				bindVars["terminal_0"] = ""
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
			test.mutate(&sequence, &terminal, &trace, bindVars, source)
			if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err == nil {
				t.Fatal("invalid row lineage trace unexpectedly validated")
			}
		})
	}
}

func TestPhysicalRelatedRowLineageTraceRequiresIndexedRootKeyFilter(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]PhysicalOperation) []PhysicalOperation
	}{
		{
			name: "root key filter removed",
			mutate: func(operations []PhysicalOperation) []PhysicalOperation {
				return []PhysicalOperation{operations[0], operations[len(operations)-1]}
			},
		},
		{
			name: "root key filter bound to another identity",
			mutate: func(operations []PhysicalOperation) []PhysicalOperation {
				copy := append([]PhysicalOperation(nil), operations...)
				filter := *copy[1].Filter
				predicate := filter.Predicate
				right := *predicate.Right
				right.BindKey = "other_root_key"
				predicate.Right = &right
				filter.Predicate = predicate
				copy[1].Filter = &filter
				return copy
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, trace, bindVars, source := validRelatedLineageFixture()
			source = test.mutate(source)
			if err := validatePhysicalRelatedRowLineageTrace(sequence, terminal, trace, source, bindVars); err == nil {
				t.Fatal("trace without its exact root-key source filter unexpectedly validated")
			}
		})
	}
}

func TestClonePhysicalPlanCopiesRowLineageTraceStages(t *testing.T) {
	plan := PhysicalPlan{StageSequence: &PhysicalStageSequence{RowLineageReturn: &PhysicalRowLineageReturn{
		Trace: &PhysicalRowLineageTrace{RootKeyBindKey: "root_key", Stages: []PhysicalRowLineageStageMatch{{
			StageID: "related_one", Kind: PhysicalStageRelatedExpandOp,
			StageRowIDBindKey: "row_one", RelatedTerminalIDBindKey: "terminal_one", RelatedRowKind: "RELATED",
			IdentityKeyBindKeys: []string{"owner_key"},
		}}},
	}}}
	cloned := ClonePhysicalPlan(plan)
	cloned.StageSequence.RowLineageReturn.Trace.Stages[0].StageID = "changed"
	cloned.StageSequence.RowLineageReturn.Trace.Stages[0].IdentityKeyBindKeys[0] = "changed_key"
	if plan.StageSequence.RowLineageReturn.Trace.Stages[0].StageID != "related_one" {
		t.Fatal("mutating a cloned trace changed the source Trace.Stages backing array")
	}
	if plan.StageSequence.RowLineageReturn.Trace.Stages[0].IdentityKeyBindKeys[0] != "owner_key" {
		t.Fatal("mutating a cloned trace changed an owner key bind in the source plan")
	}
	if cloned.StageSequence.RowLineageReturn.Trace == plan.StageSequence.RowLineageReturn.Trace {
		t.Fatal("cloned plan shares the row lineage Trace pointer")
	}
}

func TestClonePhysicalPlanCopiesPopulationMappingReturn(t *testing.T) {
	plan := PhysicalPlan{StageSequence: &PhysicalStageSequence{PopulationMappingReturn: &PhysicalStagePopulationMappingReturn{
		SourceRootKeyColumn: "_key", SourceMemberIDsColumn: PhysicalPopulationMappingMembersColumn,
		FinalRootContributorColumn: "__loom_root_contributor_keys", FinalRootContributorsMany: true,
		RowIdentityColumn: "__loom_row_id",
	}}}
	cloned := ClonePhysicalPlan(plan)
	if cloned.StageSequence.PopulationMappingReturn == plan.StageSequence.PopulationMappingReturn {
		t.Fatal("cloned plan shares the population mapping terminal pointer")
	}
	cloned.StageSequence.PopulationMappingReturn.SourceRootKeyColumn = "changed"
	if plan.StageSequence.PopulationMappingReturn.SourceRootKeyColumn != "_key" {
		t.Fatal("mutating the cloned population mapping terminal changed the source plan")
	}
}

func validRelatedLineageFixture() (PhysicalStageSequence, PhysicalRowLineageReturn, PhysicalRowLineageTrace, map[string]any, []PhysicalOperation) {
	sequence := PhysicalStageSequence{
		SourceStageID: "source", SourceRowIdentity: "_key", FinalStageID: "related_two", FinalRowIdentity: "related_row_two",
		Stages: []PhysicalConstructionStage{
			{ID: "related_one", Kind: PhysicalStageRelatedExpandOp, RowIdentityColumn: "related_row_one", RelatedExpand: &PhysicalStageRelatedExpand{
				AnchorKind: "root", AnchorColumnID: "_key", ParentIdentityColumn: "_key", TargetResourceType: "Observation", EmptyPolicy: PhysicalUnnestExclude,
			}},
			{ID: "related_two", Kind: PhysicalStageRelatedExpandOp, RowIdentityColumn: "related_row_two", RelatedExpand: &PhysicalStageRelatedExpand{
				AnchorKind: "activeRelatedRecord", AnchorColumnID: "active_observation", ParentIdentityColumn: "related_row_one", TargetResourceType: "Specimen", EmptyPolicy: PhysicalUnnestExclude,
			}},
		},
	}
	trace := PhysicalRowLineageTrace{RootKeyBindKey: "root_key", Stages: []PhysicalRowLineageStageMatch{
		{StageID: "related_one", Kind: PhysicalStageRelatedExpandOp, StageRowIDBindKey: "row_one", RelatedTerminalIDBindKey: "terminal_0", RelatedRowKind: "RELATED"},
		{StageID: "related_two", Kind: PhysicalStageRelatedExpandOp, StageRowIDBindKey: "row_two", RelatedTerminalIDBindKey: "terminal_1", RelatedRowKind: "RELATED"},
	}}
	terminal := PhysicalRowLineageReturn{ResourceType: "Patient", Trace: &trace}
	bindVars := map[string]any{
		"root_collection": "Patient", "root_key": "patient-key", "row_one": "row-id-one", "row_two": "row-id-two",
		"terminal_0": "Observation/observation-key", "terminal_1": "Specimen/specimen-key",
	}
	rootFilter := PhysicalOperation{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Predicate: PhysicalPredicate{
		Operator: "EQUALS", Left: PhysicalValue{Variable: "root", Path: []string{"_key"}}, Right: &PhysicalValue{BindKey: "root_key"},
	}}}
	source := []PhysicalOperation{
		{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		rootFilter,
		{Kind: PhysicalReturnOp, Return: &PhysicalReturn{}},
	}
	return sequence, terminal, trace, bindVars, source
}

func TestPhysicalRelatedCodingGroupLineageAcceptsExactRootPreimage(t *testing.T) {
	sequence, terminal, source, binds := relatedCodingGroupLineageFixture(t)
	if err := validatePhysicalStageRowLineage(sequence, terminal, source, binds); err != nil {
		t.Fatalf("exact direct-root related Coding Group lineage was rejected: %v", err)
	}
}

func TestPhysicalRelatedCodingGroupLineageRejectsOtherOccurrenceShapes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*PhysicalStageSequence, *PhysicalRowLineageReturn, []PhysicalOperation)
	}{
		{
			name: "expanded root identity",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ []PhysicalOperation) {
				sequence.SourceRowIdentity = "__loom_expansion_identity"
			},
		},
		{
			name: "wrong occurrence key",
			mutate: func(_ *PhysicalStageSequence, terminal *PhysicalRowLineageReturn, _ []PhysicalOperation) {
				terminal.OccurrenceKeyColumn = "other_occurrence"
			},
		},
		{
			name: "independent RowValue route",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ []PhysicalOperation) {
				sequence.Stages[0].Group.RowValues[0].FieldSource.RelatedOccurrence.RouteID = "other-route"
			},
		},
		{
			name: "contributor array",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ []PhysicalOperation) {
				sequence.Stages[0].Group.RootContributorOutputColumn = "root_contributors"
			},
		},
		{
			name: "source unnest",
			mutate: func(_ *PhysicalStageSequence, _ *PhysicalRowLineageReturn, source []PhysicalOperation) {
				source[1] = PhysicalOperation{Kind: PhysicalUnnestOp, Unnest: &PhysicalUnnest{OutputVariable: "item"}}
			},
		},
		{
			name: "non row count aggregate",
			mutate: func(sequence *PhysicalStageSequence, _ *PhysicalRowLineageReturn, _ []PhysicalOperation) {
				sequence.Stages[0].Group.Aggregates[0].Operation = "SUM"
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			sequence, terminal, source, binds := relatedCodingGroupLineageFixture(t)
			test.mutate(&sequence, &terminal, source)
			if err := validatePhysicalStageRowLineage(sequence, terminal, source, binds); err == nil {
				t.Fatal("unsupported related Coding Group lineage shape unexpectedly validated")
			}
		})
	}
}

func relatedCodingGroupLineageFixture(t *testing.T) (PhysicalStageSequence, PhysicalRowLineageReturn, []PhysicalOperation, map[string]any) {
	t.Helper()
	codingField, adapter := groupRelatedCodingFieldSourceForTest(t)
	related := *codingField.RelatedOccurrence
	statusSelector, err := spec.ParseSelector("status")
	if err != nil {
		t.Fatal(err)
	}
	statusProjection := PhysicalExpression{
		Kind: PhysicalExtractExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
		Extract: &PhysicalExtract{
			Source:       PhysicalValue{Variable: "observation_doc", Path: []string{"payload"}},
			ResourceType: "Observation", Selector: statusSelector, ExecutionMode: PhysicalSelectorDirectScalar,
		},
	}
	adapter.ScalarProjections = []PhysicalStageGroupRelatedProjection{{
		InputColumn: "related_status", ResultField: "status_result", OwnerResourceType: "Observation",
		ValueResourceType: "Observation", CandidateID: "status-candidate", CanonicalPath: "status",
	}}
	adapter.RelatedRecords.RelatedRecords.Return.Object.Fields = append(
		adapter.RelatedRecords.RelatedRecords.Return.Object.Fields,
		PhysicalExpressionProjection{Name: "status_result", Expression: statusProjection},
	)
	scalarRelated := related
	scalarRelated.ResourceType = "Observation"
	scalarRelated.CandidateID = "status-candidate"
	scalarRelated.CanonicalPath = "status"
	scalarRelated.ProjectionColumn = "related_status"
	scalarRelated.CodingSelector = ""
	scalarRelated.CodingEmptyPolicy = ""
	scalarRelated.OrdinalSemantics = ""
	scalarRelated.MembershipColumn = ""
	scalarField := &PhysicalStageGroupFieldSource{
		Kind:              PhysicalStageGroupFieldSourceRelatedOccurrence,
		RelatedOccurrence: &scalarRelated,
	}
	stage := PhysicalConstructionStage{
		ID: "group_related", InputStageID: "source_projection", Kind: PhysicalStageGroupOp,
		InputRowVariable: "group_input", OutputRowVariable: "group_output", RowIdentityColumn: "group_identity",
		InputColumns: []PhysicalStageColumn{
			{ID: "_key", Name: "_key", Kind: "string", Cardinality: "required_one", Internal: true, RootContributorResourceType: "Patient"},
			{ID: "related_status", Name: "related_status", Kind: "string", Cardinality: "optional_one", Internal: true},
			{ID: adapter.CodingMembershipColumn, Name: adapter.CodingMembershipColumn, Kind: "object", Cardinality: "optional_one", Internal: true},
			{ID: adapter.OccurrenceKeyColumn, Name: adapter.OccurrenceKeyColumn, Kind: "object", Cardinality: "required_one", Internal: true},
		},
		InputProjections: []PhysicalProjection{
			{Name: "_key", Hidden: true, Value: PhysicalValue{Variable: "group_input", Path: []string{"_key"}}},
			{Name: "related_status", Hidden: true, Value: PhysicalValue{Variable: adapter.RelatedRecords.ItemVariable, Path: []string{"status_result"}}},
			{Name: adapter.CodingMembershipColumn, Hidden: true, Expression: &PhysicalExpression{Kind: PhysicalObjectExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull, Object: &PhysicalObject{}}},
			{Name: adapter.OccurrenceKeyColumn, Hidden: true, Expression: &PhysicalExpression{Kind: PhysicalObjectExpression, Cardinality: PhysicalObjectCardinality, NullBehavior: PhysicalPreserveNull, Object: &PhysicalObject{}}},
		},
		Group: &PhysicalStageGroup{
			RelatedOccurrence: adapter,
			Keys:              []PhysicalStageGroupKey{{InputColumn: "related_status", OutputColumn: "status_group", Variable: "status_key", Kind: "STRING", FieldSource: scalarField}},
			CodingKey: &PhysicalStageGroupCodingKey{
				OutputColumn: "coding_group", InputTupleVariable: "coding_input_tuple", TupleVariable: "coding_tuple",
				LabelVariable: "coding_label", MembershipColumn: adapter.CodingMembershipColumn,
				SourceStageID: "source_projection", SourceShapeID: related.SourceShapeID,
				SourceOccurrenceKeyColumn: adapter.OccurrenceKeyColumn, RelatedSource: &related,
			},
			Aggregates: []PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "row_count", Variable: "row_count"}},
			RowValues: []PhysicalStageRowValue{{
				InputColumn: "related_status", InputKind: "STRING", Output: "statuses", Policy: "ALL",
				Variable: "statuses", FieldSource: scalarField,
			}},
		},
	}
	sequence := PhysicalStageSequence{
		SourceStageID: "source_projection", SourceRowIdentity: "_key", FinalStageID: stage.ID,
		FinalRowIdentity: stage.RowIdentityColumn, Stages: []PhysicalConstructionStage{stage},
	}
	terminal := PhysicalRowLineageReturn{
		RowIDBindKey: "row_id", OffsetBindKey: "offset", LimitBindKey: "limit", FetchLimitBindKey: "fetch_limit",
		ResourceType: "Patient", ResourceIDColumn: "resource_id", OccurrenceKeyColumn: adapter.OccurrenceKeyColumn,
	}
	source := []PhysicalOperation{
		{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{
			{Name: "_key", Hidden: true, Value: PhysicalValue{Variable: "root", Path: []string{"_key"}}},
			{Name: "resource_id", Hidden: true, Value: PhysicalValue{Variable: "root", Path: []string{"payload", "id"}}},
		}}},
	}
	binds := map[string]any{
		"root_collection": "Patient", "row_id": "group-row", "offset": 0, "limit": 10, "fetch_limit": 11,
	}
	return sequence, terminal, source, binds
}

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
