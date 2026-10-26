package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestTerminalSourceGroupPreviewPagesEligibleRootKeysBeforeCanonicalExpansion(t *testing.T) {
	plan := sourceGroupPreviewTestPlan()
	sourcePlan := ir.ClonePhysicalPlan(plan)
	sourcePlan.StageSequence = nil
	frontierPlan, _, ok := terminalSourceGroupPreviewWindowFor(
		plan, sourcePlan, plan.StageSequence, plan.StageSequence.Stages, physicalRenderOptions{}, false,
	)
	if !ok {
		t.Fatal("test source Group did not satisfy the terminal key-window shape")
	}
	if len(frontierPlan.Operations) != 6 || frontierPlan.Operations[5].Kind != ir.PhysicalReturnOp ||
		len(frontierPlan.Operations[5].Return.Projections) != 2 ||
		frontierPlan.Operations[5].Return.Projections[0].Name != sourceGroupPreviewKeyColumn ||
		frontierPlan.Operations[5].Return.Projections[1].Name != sourceGroupPreviewRootKey {
		t.Fatalf("frontier did not reduce the source to its direct key and root identity: %#v", frontierPlan.Operations)
	}

	rendered, err := renderPhysicalStageSequence(plan, physicalRenderOptions{})
	if err != nil {
		t.Fatalf("renderPhysicalStageSequence() error = %v", err)
	}
	query := rendered.Query
	frontier := strings.Index(query, "roots_by_key")
	badKeyCheck := strings.Index(query, "bad_key_groups")
	selectedKeyWindow := strings.Index(query, "selected_root_keys = FLATTEN(")
	canonicalSource := strings.Index(query, "LET __loom_construction_stage_1 = (")
	if frontier < 0 || badKeyCheck < frontier || selectedKeyWindow < badKeyCheck || canonicalSource < selectedKeyWindow {
		t.Fatalf("group-key selection, global type sentinel, and canonical source are out of order:\n%s", query)
	}
	if strings.Contains(query[:canonicalSource], "unused_source_value") {
		t.Fatalf("source-only post-expansion expression leaked into the root key frontier:\n%s", query[:canonicalSource])
	}
	for _, want := range []string{
		"FILTER ASSERT(LENGTH(__loom_physical_construction_source_group_preview_bad_key_groups) == 0, \"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH\")",
		"TYPENAME(__loom_physical_construction_source_group_preview_bad_key_group.group_key)",
		"SORT __loom_physical_construction_source_group_preview_group.group_key ASC",
		"FOR __loom_physical_preview_root_key IN __loom_physical_construction_source_group_preview_selected_root_keys",
		"LET root = DOCUMENT(@@root_collection, __loom_physical_preview_root_key)",
		"FOR observation, edge IN 1..1 INBOUND",
		"FILTER edge.dataset_generation == @dataset_generation",
		"FILTER observation.dataset_generation == @dataset_generation",
		"edge.auth_resource_path IN @auth_resource_paths AND observation.auth_resource_path IN @auth_resource_paths",
		"COLLECT group_key_value = group_input[@",
		"WITH COUNT INTO",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("preview query is missing %q:\n%s", want, query)
		}
	}
	if got := strings.Count(query, "FILTER root.project == @project"); got != 2 {
		t.Fatalf("root project filter rendered %d times, want once in frontier and once in canonical source:\n%s", got, query)
	}
}

func TestTerminalSourceGroupPreviewFallsBackOutsideNarrowContract(t *testing.T) {
	testCases := map[string]func(*ir.PhysicalPlan){
		"exclude missing keys": func(plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.MissingKeyPolicy = ir.PhysicalStageGroupMissingKeyExclude
		},
		"error on missing keys": func(plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.MissingKeyPolicy = ir.PhysicalStageGroupMissingKeyError
		},
		"inner unnest": func(plan *ir.PhysicalPlan) {
			plan.Operations[5].Unnest.EmptyPolicy = ir.PhysicalUnnestExclude
		},
		"row value contributor reduction": func(plan *ir.PhysicalPlan) {
			plan.StageSequence.Stages[0].Group.RowValues = []ir.PhysicalStageRowValue{{InputColumn: "value", InputKind: "STRING", Policy: "ALL"}}
		},
	}
	for name, change := range testCases {
		t.Run(name, func(t *testing.T) {
			plan := sourceGroupPreviewTestPlan()
			change(&plan)
			sourcePlan := ir.ClonePhysicalPlan(plan)
			sourcePlan.StageSequence = nil
			if _, _, ok := terminalSourceGroupPreviewWindowFor(
				plan, sourcePlan, plan.StageSequence, plan.StageSequence.Stages, physicalRenderOptions{}, false,
			); ok {
				t.Fatal("unsupported source Group shape entered the key-window fast path")
			}
		})
	}
}

func sourceGroupPreviewTestPlan() ir.PhysicalPlan {
	root := func(path ...string) ir.PhysicalValue { return ir.PhysicalValue{Variable: "root", Path: path} }
	groupInput := func(path ...string) ir.PhysicalValue { return ir.PhysicalValue{Variable: "group_input", Path: path} }
	traversal := ir.PhysicalTraversal{
		SourceVariable: "root", TargetVariable: "observation", EdgeVariable: "edge",
		Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "route_edges", EdgeLabelBindKey: "route_label",
		TargetTypeBindKey: "route_target_type", EdgeTargetTypeField: "from_type",
	}
	routeScope := []ir.PhysicalOperation{
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "edge", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "observation", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "edge", Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "observation", Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
			Variable: "route_scope", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
			Inputs: []ir.PhysicalValue{{Variable: "edge", Path: []string{"auth_resource_path"}}, {Variable: "observation", Path: []string{"auth_resource_path"}}, {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}},
		}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "route_scope"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
	}
	return ir.PhysicalPlan{
		Version: 1, Engine: ir.PhysicalEngineAQL, Source: ir.PhysicalSource{ResourceType: "Specimen"},
		BindVars: map[string]any{
			"root_collection": "Specimen", "route_edges": "fhir_edge", "route_label": "specimen_Observation", "route_target_type": "Observation",
			"project": "loom", "dataset_generation": "gen-1", "scope_allowed": true,
			"auth_resource_paths": []string{"root/*"}, "auth_resource_paths_unrestricted": false,
			"construction_id": "construction", "preview_limit": 25,
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: root("project"), Right: &ir.PhysicalValue{BindKey: "project"}}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: root("dataset_generation"), Right: &ir.PhysicalValue{BindKey: "dataset_generation"}}}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
				Variable: "root_scope_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
				Inputs: []ir.PhysicalValue{root("auth_resource_path"), {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"}},
			}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root_scope_allowed"}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"}}}},
			{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
				Owner: ir.PhysicalUnnestOwner{OccurrenceID: "observation", ResourceType: "Observation", RootVariable: "root", OwnerVariable: "observation",
					Route: []ir.PhysicalUnnestRouteStep{{OccurrenceID: "observation", Traversal: traversal, Scope: routeScope}}},
				OutputVariable: "coding", HasItemVariable: "has_coding",
				Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
					NullBehavior: ir.PhysicalEmptyOnNull, Value: ptrPhysicalValue(ir.PhysicalValue{Variable: "observation", Path: []string{"payload", "valueCodeableConcept", "coding"}})},
				EmptyPolicy: ir.PhysicalUnnestPreserveParent,
			}},
			{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
				Variable: "unused_source_value",
				Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
					NullBehavior: ir.PhysicalPreserveNull, Value: ptrPhysicalValue(root("payload", "expensive_unused_value"))},
			}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "subject_reference", Value: root("payload", "subject", "reference")}}}},
		},
		StageSequence: &ir.PhysicalStageSequence{
			SourceStageID: "source", FinalStageID: "group", FinalRowIdentity: "row_id", PreviewLimitBindKey: "preview_limit",
			SourceColumns: []ir.PhysicalStageColumn{{ID: "subject-reference", Name: "subject_reference", Kind: "string", Cardinality: "optional_one"}},
			FinalColumns: []ir.PhysicalStageColumn{
				{ID: "subject-reference-output", Name: "subject_reference", Kind: "string", Cardinality: "optional_one"},
				{ID: "count-output", Name: "source_records", Kind: "integer", Cardinality: "required_one"},
				{ID: "identity", Name: "row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
			},
			Stages: []ir.PhysicalConstructionStage{{
				ID: "group", InputStageID: "source", Kind: ir.PhysicalStageGroupOp,
				InputRowVariable: "group_input", OutputRowVariable: "group_output", InputColumns: []ir.PhysicalStageColumn{{ID: "subject-reference", Name: "subject_reference", Kind: "string", Cardinality: "optional_one"}},
				OutputColumns: []ir.PhysicalStageColumn{
					{ID: "subject-reference-output", Name: "subject_reference", Kind: "string", Cardinality: "optional_one"},
					{ID: "count-output", Name: "source_records", Kind: "integer", Cardinality: "required_one"},
					{ID: "identity", Name: "row_id", Kind: "string", Cardinality: "required_one", Internal: true, Identity: true},
				},
				InputProjections: []ir.PhysicalProjection{{Name: "subject_reference", Value: groupInput("subject_reference")}},
				OutputProjections: []ir.PhysicalProjection{
					{Name: "subject_reference", Value: ir.PhysicalValue{Variable: "group_key_value"}},
					{Name: "source_records", Value: ir.PhysicalValue{Variable: "source_count"}},
					{Name: "row_id", Hidden: true, Value: ir.PhysicalValue{Variable: "group_identity"}},
				},
				Group: &ir.PhysicalStageGroup{
					GroupRowsVariable: "group_rows", IdentityVariable: "group_identity", ConstructionIDBindKey: "construction_id",
					MissingKeyPolicy: ir.PhysicalStageGroupMissingKeyGroup,
					Keys:             []ir.PhysicalStageGroupKey{{InputColumn: "subject_reference", OutputColumn: "subject_reference", Variable: "group_key_value", Kind: "STRING"}},
					Aggregates:       []ir.PhysicalStageGroupAggregate{{Operation: "COUNT_ROWS", Output: "source_records", Variable: "source_count", OutputKind: "integer"}},
				},
			}},
		},
	}
}
