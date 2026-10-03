package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

func TestCodedGroupSourceInliningUsesPhysicalRootScanVariable(t *testing.T) {
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "selected_root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
	}}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source_projection",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group_codes", InputStageID: "source_projection", Kind: ir.PhysicalStageCodedGroupOp,
			CodedGroup: &ir.PhysicalStageCodedGroup{
				RootCollectionBindKey: "root_collection", SourceIdentityColumn: "_key", SourceRowsUnique: true,
			},
		}},
	}
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{}); got != "selected_root" {
		t.Fatalf("inline root variable = %q, want physical RootScan variable %q", got, "selected_root")
	}
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{terminalProjectionColumn: "source_records"}); got != "" {
		t.Fatalf("terminal-projection render selected inline root %q, want fallback", got)
	}
	plan.Operations = append(plan.Operations[:1], ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp}, plan.Operations[1])
	if got := codedGroupSourceRootVariable(plan, sequence, sequence.Stages, physicalRenderOptions{}); got != "" {
		t.Fatalf("cardinality-changing source selected inline root %q, want fallback", got)
	}
}

func TestSourceGroupPrunesUnusedRelatedSetAndKeepsFilters(t *testing.T) {
	traversal := func(target, edge, collection, label, resourceType string) ir.PhysicalOperation {
		return ir.PhysicalOperation{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
			SourceVariable: "root", TargetVariable: target, EdgeVariable: edge,
			Direction: ir.PhysicalInbound, EdgeCollectionBindKey: collection,
			EdgeLabelBindKey: label, TargetTypeBindKey: resourceType,
			EdgeTargetTypeField: "from_type",
		}}
	}
	valueExpression := func(value ir.PhysicalValue) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &value}
	}
	plan := ir.PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection":                  "Specimen",
			"project":                          "loom",
			"dataset_generation":               "generation-1",
			"scope_allowed":                    true,
			"auth_resource_paths":              []string{"root/*"},
			"auth_resource_paths_unrestricted": false,
			"required_edges":                   "edge_eligibility",
			"required_label":                   "specimen_Condition",
			"required_resource_type":           "Condition",
			"required_key":                     "eligible",
			"child_edges":                      "edge_child",
			"child_label":                      "specimen_Patient",
			"child_resource_type":              "Patient",
		},
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}},
				Right: &ir.PhysicalValue{BindKey: "project"},
			}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"dataset_generation"}},
				Right: &ir.PhysicalValue{BindKey: "dataset_generation"},
			}}},
			{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
				Variable: "root_scope_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
				Inputs: []ir.PhysicalValue{
					{Variable: "root", Path: []string{"auth_resource_path"}},
					{BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"},
				},
			}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root_scope_allowed"},
				Right: &ir.PhysicalValue{BindKey: "scope_allowed"},
			}}},
			{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
				Variable: "required_coded_map",
				Expression: ir.PhysicalExpression{Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality,
					NullBehavior: ir.PhysicalPreserveNull,
					Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{{
						Name: "eligible", Expression: valueExpression(ir.PhysicalValue{Variable: "root", Path: []string{"payload", "eligibility"}}),
					}}},
				},
			}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EXISTS",
				LeftExpression: &ir.PhysicalExpression{Kind: ir.PhysicalObjectLookupExpression,
					Cardinality: ir.PhysicalArrayCardinality, NullBehavior: ir.PhysicalPreserveNull,
					ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "required_coded_map", KeyBindKey: "required_key"}},
			}}},
			{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
				Kind: ir.PhysicalExistsPredicate,
				Exists: &ir.PhysicalSubplan{
					Captures:   []string{"root"},
					Operations: []ir.PhysicalOperation{traversal("eligibility_child", "eligibility_edge", "required_edges", "required_label", "required_resource_type")},
					Return:     valueExpression(ir.PhysicalValue{Variable: "eligibility_child", Path: []string{"_key"}}),
				},
			}}},
			{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
				Variable: "unused_child_set", Kind: ir.PhysicalNodeSetKind,
				Subplan: ir.PhysicalSubplan{
					Captures:   []string{"root"},
					Operations: []ir.PhysicalOperation{traversal("unused_child", "unused_edge", "child_edges", "child_label", "child_resource_type")},
					Return:     valueExpression(ir.PhysicalValue{Variable: "unused_child", Path: []string{"_key"}}),
				},
			}},
			{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
				Variable: "unused_correlation", Expression: valueExpression(ir.PhysicalValue{Variable: "unused_child_set"}),
			}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
				{
					Name: "body_site",
					Expression: &ir.PhysicalExpression{
						Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull,
						Extract: &ir.PhysicalExtract{
							Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Specimen",
							Selector: fhirschema.Selector{Steps: []fhirschema.SelectorStep{
								{Field: "collection"}, {Field: "bodySite"}, {Field: "reference"}, {Field: "reference"},
							}},
							ExecutionMode: ir.PhysicalSelectorDirectScalar,
						},
					},
					Presence: &ir.PhysicalProjectionPresence{
						Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}},
						Paths:  [][]string{{"collection", "bodySite", "reference", "reference"}},
					},
				},
				{Name: "unused_column", Value: ir.PhysicalValue{Variable: "unused_child_set"}},
			}}},
		},
	}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group", InputStageID: "source", Kind: ir.PhysicalStageGroupOp,
			Group: &ir.PhysicalStageGroup{Keys: []ir.PhysicalStageGroupKey{{InputColumn: "body_site"}}},
		}},
	}

	pruneUnusedSourceGroupProjections(&plan, sequence)
	rendered, err := RenderPhysicalPlan(plan)
	if err != nil {
		t.Fatalf("RenderPhysicalPlan() error = %v", err)
	}
	for _, fragment := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"LET root_scope_allowed = @auth_resource_paths_unrestricted == true OR root.auth_resource_path IN @auth_resource_paths",
		"FILTER root_scope_allowed == @scope_allowed",
		"LET required_coded_map = { [@__loom_physical_object_field_0_name]: root.payload.eligibility }",
		"required_coded_map[@required_key]",
		"required_edges",
		"RETURN { [@__loom_physical_projection_0_name]: root.payload.collection.bodySite.reference.reference }",
	} {
		if !strings.Contains(rendered.Query, fragment) {
			t.Errorf("rendered query missing required source behavior %q:\n%s", fragment, rendered.Query)
		}
	}
	for _, fragment := range []string{"unused_child_set", "unused_correlation", "unused_column", "child_edges"} {
		if strings.Contains(rendered.Query, fragment) {
			t.Errorf("rendered query retained unused source work %q:\n%s", fragment, rendered.Query)
		}
	}
}
