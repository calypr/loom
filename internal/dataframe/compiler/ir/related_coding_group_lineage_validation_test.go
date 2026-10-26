package ir

import (
	"testing"

	"github.com/calypr/loom/internal/dataframe/spec"
)

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
