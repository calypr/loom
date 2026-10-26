package aql

import (
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestComposedRelatedRowLineageReplaysArbitraryChainAndPagesAuthoredStages(t *testing.T) {
	bindVars := map[string]any{
		"root_key": "root-1", "row_id": "final-row", "offset": 1, "limit": 2, "fetch": 3,
		"root_resource_type": "Patient",
		"root_id_column":     "__root_id", "root_key_column": "__root_key",
		"construction_1": "related-1", "construction_2": "related-2", "construction_3": "related-3",
		"stage_1_row": "stage-row-1", "stage_1_terminal": "Patient/one",
		"stage_2_row": "stage-row-2", "stage_2_terminal": "Patient/one",
		"stage_3_row": "stage-row-3", "stage_3_terminal": "",
		"edge_collection": "fhir_edge", "edge_label": "subject", "target_type": "Patient",
	}
	stage1 := composedLineageRelatedStage("related-1", "in_1", "out_1", "item_1", "identity_1", "construction_1", "bridge_1", "target_1", "Patient")
	stage2 := composedLineageRelatedStage("related-2", "in_2", "out_2", "item_2", "identity_2", "construction_2", "bridge_2", "target_2", "Patient")
	stage3 := composedLineageRelatedStage("related-3", "in_3", "out_3", "item_3", "identity_3", "construction_3", "bridge_3", "target_3", "Patient")
	stage3.RelatedExpand.EmptyPolicy = ir.PhysicalUnnestPreserveParent
	identityPass := ir.PhysicalConstructionStage{
		ID: "derive-1", Kind: ir.PhysicalStageDeriveOp, InputRowVariable: "derive_input", OutputRowVariable: "derive_output",
		RowIdentityColumn: "row_identity",
		OutputProjections: []ir.PhysicalProjection{{Name: "row_identity", Value: ir.PhysicalValue{Variable: "derive_input", Path: []string{"row_identity"}}}},
	}
	stages := []ir.PhysicalConstructionStage{stage1, identityPass, stage2, stage3}
	for index := range stages {
		stages[index].RowIdentityColumn = "row_identity"
	}
	trace := &ir.PhysicalRowLineageTrace{
		RootKeyBindKey: "root_key",
		Stages: []ir.PhysicalRowLineageStageMatch{
			{StageID: "related-1", Kind: ir.PhysicalStageRelatedExpandOp, StageRowIDBindKey: "stage_1_row", RelatedTerminalIDBindKey: "stage_1_terminal", RelatedRowKind: "RELATED"},
			{StageID: "related-2", Kind: ir.PhysicalStageRelatedExpandOp, StageRowIDBindKey: "stage_2_row", RelatedTerminalIDBindKey: "stage_2_terminal", RelatedRowKind: "RELATED"},
			{StageID: "related-3", Kind: ir.PhysicalStageRelatedExpandOp, StageRowIDBindKey: "stage_3_row", RelatedTerminalIDBindKey: "stage_3_terminal", RelatedRowKind: "EMPTY"},
		},
	}
	terminal := ir.PhysicalRowLineageReturn{
		RowIDBindKey: "row_id", OffsetBindKey: "offset", LimitBindKey: "limit", FetchLimitBindKey: "fetch",
		ResourceType: "Patient", ResourceIDColumn: "__root_id", OccurrenceKeyColumn: "__root_key", Trace: trace,
	}
	renderer := &physicalPlanRenderer{
		bindVars: bindVars, reservedVars: map[string]struct{}{}, internalPrefix: "composed_lineage_test_",
	}
	plan, err := renderer.renderConstructionRowLineage(
		"FOR root IN @@roots\nFILTER root._key == @root_key\nRETURN {row_identity: root._key, __root_id: root.payload.id, __root_key: root._key}",
		stages, terminal,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"FILTER target_1._id == @stage_1_terminal",
		"FILTER target_2._id == @stage_2_terminal",
		"FILTER item_3 == null",
		"FILTER out_1[@", "== @stage_1_row",
		"FILTER out_2[@", "== @stage_2_row",
		"FILTER out_3[@", "== @stage_3_row",
		"== @row_id",
		"FOR __loom_physical_composed_lineage_test_row_lineage_composed_ordinal IN 0..3",
		"LIMIT @offset, @fetch",
		"SLICE(", "@limit", "hasMore:",
		"PARSE_IDENTIFIER(item_1.terminal_id).key", "PARSE_IDENTIFIER(item_2.terminal_id).key",
	} {
		if !strings.Contains(plan.Query, want) {
			t.Errorf("composed lineage query missing %q:\n%s", want, plan.Query)
		}
	}
	if strings.Contains(strings.ToUpper(plan.Query), "COLLECT INTO") {
		t.Fatalf("composed lineage query materializes a route frontier:\n%s", plan.Query)
	}
	for _, stageItem := range []string{"item_1", "item_2"} {
		if !strings.Contains(plan.Query, "resourceId: "+stageItem+".resource_id") {
			t.Errorf("authored contributor for %s was collapsed:\n%s", stageItem, plan.Query)
		}
	}
}

func composedLineageRelatedStage(
	id, input, output, item, identity, constructionID, bridge, target, resourceType string,
) ir.PhysicalConstructionStage {
	terminalTypeBind := "target_type"
	traversals := []ir.PhysicalOperation{
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: input, TargetVariable: bridge, EdgeVariable: "edge_" + bridge,
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection", EdgeLabelBindKey: "edge_label",
			TargetTypeBindKey: terminalTypeBind, EdgeTargetTypeField: "to_type",
		}},
		{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: bridge, TargetVariable: target, EdgeVariable: "edge_" + target,
			Direction: ir.PhysicalOutbound, EdgeCollectionBindKey: "edge_collection", EdgeLabelBindKey: "edge_label",
			TargetTypeBindKey: terminalTypeBind, EdgeTargetTypeField: "to_type",
		}},
	}
	terminal := func(field string) ir.PhysicalExpression {
		return ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Value: &ir.PhysicalValue{Variable: target, Path: []string{field}},
		}
	}
	return ir.PhysicalConstructionStage{
		ID: id, Kind: ir.PhysicalStageRelatedExpandOp, InputRowVariable: input, OutputRowVariable: output,
		RowIdentityColumn: "row_identity",
		OutputProjections: []ir.PhysicalProjection{{Name: "row_identity", Value: ir.PhysicalValue{Variable: identity}}},
		RelatedExpand: &ir.PhysicalStageRelatedExpand{
			TargetResourceType: resourceType, ParentIdentityColumn: "row_identity", ConstructionIDBindKey: constructionID,
			ItemVariable: item, IdentityVariable: identity, EmptyPolicy: ir.PhysicalUnnestError,
			RelatedRecords: ir.PhysicalSubplan{
				Captures: []string{input}, Operations: traversals,
				Return: ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
						{Name: "terminal_id", Expression: terminal("_id")}, {Name: "resource_id", Expression: terminal("id")},
					}}},
			},
		},
	}
}

func TestComposedReplayRendersRelatedSourceEligibilityAndFieldOperations(t *testing.T) {
	baseSubplan := ir.PhysicalSubplan{
		Captures: []string{"input_row"},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalCollectionScanOp, CollectionScan: &ir.PhysicalCollectionScan{Variable: "source_item", CollectionBindKey: "source_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS",
				Left:     ir.PhysicalValue{Variable: "source_item", Path: []string{"_key"}},
				Right:    &ir.PhysicalValue{Variable: "input_row", Path: []string{"_key"}},
			}}},
		},
		Return: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Value: &ir.PhysicalValue{Variable: "source_item", Path: []string{"_id"}},
		},
	}
	countSubplan := baseSubplan
	countSubplan.Unique = true
	countSubplan.Sort = &ir.PhysicalValue{Variable: "source_item", Path: []string{"_id"}}
	countExpression := ir.PhysicalExpression{
		Kind: ir.PhysicalCallExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Call: &ir.PhysicalCall{Name: "length", Args: []ir.PhysicalExpression{{
			Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull,
			Subplan: &countSubplan,
		}}},
	}
	identityProjection := func(input string) ir.PhysicalProjection {
		return ir.PhysicalProjection{Name: "row_identity", Value: ir.PhysicalValue{Variable: input, Path: []string{"row_identity"}}}
	}
	tests := []struct {
		name      string
		stage     ir.PhysicalConstructionStage
		binds     map[string]any
		wantQuery []string
		wantBinds map[string]any
	}{
		{
			name: "RELATED_SOURCE output subplan",
			stage: ir.PhysicalConstructionStage{
				ID: "source-stage", Kind: ir.PhysicalStageRelatedSourceOp, InputRowVariable: "input_row", OutputRowVariable: "source_output", RowIdentityColumn: "row_identity",
				RelatedSource: &ir.PhysicalStageRelatedSource{AnchorColumnID: "anchor", OutputColumnID: "related-output", Form: "ALL"},
				OutputProjections: []ir.PhysicalProjection{
					identityProjection("input_row"),
					{Name: "related_output", Expression: &ir.PhysicalExpression{Kind: ir.PhysicalSubplanExpression, Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull, Subplan: &baseSubplan}},
				},
			},
			binds:     map[string]any{"source_collection": "source_records"},
			wantQuery: []string{"FOR source_item IN @@source_collection", "source_item._key == input_row._key", "RETURN source_item._id"},
			wantBinds: map[string]any{"source_collection": "source_records"},
		},
		{
			name: "RELATED_ELIGIBILITY count LET and filter",
			stage: ir.PhysicalConstructionStage{
				ID: "eligibility-stage", Kind: ir.PhysicalStageRelatedEligibilityOp, InputRowVariable: "input_row", OutputRowVariable: "eligible_output", RowIdentityColumn: "row_identity",
				DerivedLets: []ir.PhysicalOperation{{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "related_count", Expression: countExpression}}},
				Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
					Kind:       ir.PhysicalComparisonPredicate,
					Comparison: &ir.PhysicalPredicate{Operator: "GTE", Left: ir.PhysicalValue{Variable: "related_count"}, Right: &ir.PhysicalValue{BindKey: "minimum_count"}},
				}},
				OutputProjections: []ir.PhysicalProjection{identityProjection("input_row")},
			},
			binds:     map[string]any{"source_collection": "source_records", "minimum_count": 2},
			wantQuery: []string{"LET related_count = LENGTH(FLATTEN(SORTED_UNIQUE((", "source_item._key == input_row._key", "FILTER related_count >= @minimum_count", "RETURN {"},
			wantBinds: map[string]any{"source_collection": "source_records", "minimum_count": 2},
		},
		{
			name: "RELATED_FIELD exact-document projection",
			stage: ir.PhysicalConstructionStage{
				ID: "field-stage", Kind: ir.PhysicalStageRelatedFieldOp, InputRowVariable: "input_row", OutputRowVariable: "field_output", RowIdentityColumn: "row_identity",
				RelatedField: &ir.PhysicalStageRelatedField{ActiveRecordColumn: "active_record_id", CandidateID: "candidate", TargetNodeID: "observation", TargetResourceType: "Observation", OutputColumnID: "field-output", LogicalType: "string", Path: []string{"payload", "status"}},
				OutputProjections: []ir.PhysicalProjection{
					identityProjection("input_row"),
					{Name: "related_status", Expression: &ir.PhysicalExpression{
						Kind: ir.PhysicalRelatedFieldExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
						RelatedField: &ir.PhysicalRelatedField{DocumentID: ir.PhysicalValue{Variable: "input_row", Path: []string{"active_record_id"}}, ResourceType: "Observation", Path: []string{"payload", "status"}},
					}},
				},
			},
			binds:     map[string]any{"project": "project-a", "dataset_generation": "generation-a", "auth_resource_paths": []string{"Observation/visible"}, "auth_resource_paths_unrestricted": false},
			wantQuery: []string{"DOCUMENT(input_row.active_record_id)", ".project == @project", ".dataset_generation == @dataset_generation", ".resourceType == @", "auth_resource_path IN @auth_resource_paths"},
			wantBinds: map[string]any{"project": "project-a", "dataset_generation": "generation-a", "auth_resource_paths": []string{"Observation/visible"}, "auth_resource_paths_unrestricted": false},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bindVars := map[string]any{}
			for key, value := range test.binds {
				bindVars[key] = value
			}
			renderer := &physicalPlanRenderer{bindVars: bindVars, reservedVars: map[string]struct{}{}, internalPrefix: "related_identity_replay_test_"}
			_, lines, err := renderer.renderComposedLineageProjectionStage("prior_rows", test.stage, nil)
			if err != nil {
				t.Fatal(err)
			}
			query := strings.Join(lines, "\n")
			for _, want := range test.wantQuery {
				if !strings.Contains(query, want) {
					t.Errorf("replayed %s query missing %q:\n%s", test.name, want, query)
				}
			}
			for key, want := range test.wantBinds {
				if got, ok := renderer.bindVars[key]; !ok || !reflect.DeepEqual(got, want) {
					t.Errorf("replayed %s bind %q = %#v, want %#v", test.name, key, got, want)
				}
			}
			if test.stage.Kind == ir.PhysicalStageRelatedFieldOp {
				if got := renderer.bindVars["__loom_physical_related_identity_replay_test_related_field_resource_type"]; got != "Observation" {
					t.Errorf("related-field resource type bind = %#v, want Observation", got)
				}
				if got := renderer.bindVars["__loom_physical_related_identity_replay_test_related_field_path"]; got != "status" {
					t.Errorf("related-field path bind = %#v, want status", got)
				}
			}
		})
	}
}
