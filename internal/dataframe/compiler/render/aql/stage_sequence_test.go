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
	scopedTraversal := func(operation ir.PhysicalOperation) []ir.PhysicalOperation {
		traversal := operation.Traversal
		operations := []ir.PhysicalOperation{operation}
		for _, variable := range []string{traversal.EdgeVariable, traversal.TargetVariable} {
			operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: variable, Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"},
			}}})
		}
		for _, variable := range []string{traversal.EdgeVariable, traversal.TargetVariable} {
			operations = append(operations, ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: variable, Path: []string{"dataset_generation"}}, Right: &ir.PhysicalValue{BindKey: "dataset_generation"},
			}}})
		}
		scopeVariable := traversal.TargetVariable + "_scope_allowed"
		operations = append(operations,
			ir.PhysicalOperation{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
				Variable: scopeVariable, Operator: "AUTH_RESOURCE_PATH_ALLOWED",
				Inputs: []ir.PhysicalValue{
					{Variable: traversal.EdgeVariable, Path: []string{"auth_resource_path"}},
					{Variable: traversal.TargetVariable, Path: []string{"auth_resource_path"}},
					{BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"},
				},
			}},
			ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
				Operator: "EQUALS", Left: ir.PhysicalValue{Variable: scopeVariable}, Right: &ir.PhysicalValue{BindKey: "scope_allowed"},
			}}},
		)
		return operations
	}
	valueExpression := func(value ir.PhysicalValue) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &value}
	}
	repeatedScalarProjection := func(name string, steps ...fhirschema.SelectorStep) ir.PhysicalProjection {
		return ir.PhysicalProjection{Name: name, Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Condition",
				Selector: fhirschema.Selector{Steps: steps}, ExecutionMode: ir.PhysicalSelectorConditionalArray,
			},
		}}
	}
	plan := ir.PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection":                  "Condition",
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
					Operations: scopedTraversal(traversal("eligibility_child", "eligibility_edge", "required_edges", "required_label", "required_resource_type")),
					Return:     valueExpression(ir.PhysicalValue{Variable: "eligibility_child", Path: []string{"_key"}}),
				},
			}}},
			{Kind: ir.PhysicalSetOp, Set: &ir.PhysicalSet{
				Variable: "unused_child_set", Kind: ir.PhysicalNodeSetKind,
				Subplan: ir.PhysicalSubplan{
					Captures:   []string{"root"},
					Operations: scopedTraversal(traversal("unused_child", "unused_edge", "child_edges", "child_label", "child_resource_type")),
					Return:     valueExpression(ir.PhysicalValue{Variable: "unused_child", Path: []string{"_key"}}),
				},
			}},
			{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
				Variable: "unused_correlation", Expression: valueExpression(ir.PhysicalValue{Variable: "unused_child_set"}),
			}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
				{Name: "id", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "id"}}},
				repeatedScalarProjection("code", fhirschema.SelectorStep{Field: "code"}, fhirschema.SelectorStep{Field: "coding", Iterate: true}, fhirschema.SelectorStep{Field: "code"}),
				repeatedScalarProjection("category", fhirschema.SelectorStep{Field: "category", Iterate: true}, fhirschema.SelectorStep{Field: "coding", Iterate: true}, fhirschema.SelectorStep{Field: "code"}),
				{Name: "unused_column", Value: ir.PhysicalValue{Variable: "unused_child_set"}},
			}}},
		},
	}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group", InputStageID: "source", Kind: ir.PhysicalStageGroupOp,
			Group: &ir.PhysicalStageGroup{
				Keys:      []ir.PhysicalStageGroupKey{{InputColumn: "id"}},
				RowValues: []ir.PhysicalStageRowValue{{InputColumn: "code", Policy: "ALL"}, {InputColumn: "category", Policy: "ALL"}},
			},
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
		"FILTER eligibility_edge.project == @project",
		"FILTER eligibility_child.project == @project",
		"FILTER eligibility_edge.dataset_generation == @dataset_generation",
		"FILTER eligibility_child.dataset_generation == @dataset_generation",
		"eligibility_edge.auth_resource_path IN @auth_resource_paths AND eligibility_child.auth_resource_path IN @auth_resource_paths",
		"FILTER eligibility_child_scope_allowed == @scope_allowed",
		"root.payload.code",
		"root.payload.category",
		"RETURN { [@__loom_physical_projection_0_name]: root.payload.id",
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

func TestSourceGroupPrunesUnusedSetsForRepeatedRootProjections(t *testing.T) {
	repeated := func(name string, steps ...fhirschema.SelectorStep) ir.PhysicalProjection {
		return ir.PhysicalProjection{Name: name, Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalExtractExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalPreserveNull,
			Extract: &ir.PhysicalExtract{
				Source: ir.PhysicalValue{Variable: "root", Path: []string{"payload"}}, ResourceType: "Condition",
				Selector: fhirschema.Selector{Steps: steps}, ExecutionMode: ir.PhysicalSelectorConditionalArray,
			},
		}}
	}
	set := func(variable, source string) *ir.PhysicalSet {
		captures := []string{"root"}
		if source != "" {
			captures = append(captures, source)
		}
		return &ir.PhysicalSet{Variable: variable, SourceSetVariable: source,
			Subplan: ir.PhysicalSubplan{Captures: captures}}
	}
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"},
		}}},
		{Kind: ir.PhysicalSetOp, Set: set("unused_parent", "")},
		{Kind: ir.PhysicalSetOp, Set: set("unused_child", "unused_parent")},
		{Kind: ir.PhysicalSetOp, Set: set("live_parent", "")},
		{Kind: ir.PhysicalSetOp, Set: set("live_child", "live_parent")},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalExistsPredicate, Exists: &ir.PhysicalSubplan{Captures: []string{"live_child"}},
		}}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "onset", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "onsetString"}}},
			repeated("code", fhirschema.SelectorStep{Field: "code"}, fhirschema.SelectorStep{Field: "coding", Iterate: true}, fhirschema.SelectorStep{Field: "code"}),
			repeated("category", fhirschema.SelectorStep{Field: "category", Iterate: true}, fhirschema.SelectorStep{Field: "coding", Iterate: true}, fhirschema.SelectorStep{Field: "code"}),
			{Name: "unused", Value: ir.PhysicalValue{Variable: "unused_child"}},
		}}},
	}}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source_projection",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group", InputStageID: "source_projection", Kind: ir.PhysicalStageGroupOp,
			Group: &ir.PhysicalStageGroup{
				Keys: []ir.PhysicalStageGroupKey{{InputColumn: "onset"}},
				RowValues: []ir.PhysicalStageRowValue{
					{InputColumn: "code", Policy: "ALL"}, {InputColumn: "category", Policy: "ALL"},
				},
			},
		}},
	}

	pruneUnusedSourceGroupProjections(&plan, sequence)
	sets, filters := map[string]bool{}, 0
	var sourceReturn *ir.PhysicalReturn
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSetOp:
			sets[operation.Set.Variable] = true
		case ir.PhysicalFilterOp:
			filters++
		case ir.PhysicalReturnOp:
			sourceReturn = operation.Return
		}
	}
	for _, variable := range []string{"live_parent", "live_child"} {
		if !sets[variable] {
			t.Errorf("live source set %q was pruned", variable)
		}
	}
	for _, variable := range []string{"unused_parent", "unused_child"} {
		if sets[variable] {
			t.Errorf("unused source set %q was retained", variable)
		}
	}
	if filters != 2 {
		t.Fatalf("source filters = %d, want membership and match filters retained", filters)
	}
	if sourceReturn == nil || len(sourceReturn.Projections) != 3 {
		t.Fatalf("source return = %#v, want group key and two row-value projections", sourceReturn)
	}
	for _, projection := range sourceReturn.Projections {
		if projection.Name != "code" && projection.Name != "category" {
			continue
		}
		if projection.Expression == nil || projection.Expression.Cardinality != ir.PhysicalArrayCardinality ||
			projection.Expression.Extract == nil || projection.Expression.Extract.Source.Variable != "root" {
			t.Errorf("repeated root projection %q changed: %#v", projection.Name, projection)
		}
	}
}

func TestSourceGroupPrunesUnusedOptionalRouteBesideCorrelatedPivot(t *testing.T) {
	value := func(variable string) ir.PhysicalExpression {
		return ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: variable}}
	}
	set := func(variable string, captures ...string) *ir.PhysicalSet {
		return &ir.PhysicalSet{Variable: variable, Kind: ir.PhysicalNodeSetKind,
			Subplan: ir.PhysicalSubplan{Captures: captures, Return: value("root")}}
	}
	plan := ir.PhysicalPlan{Operations: []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "root", Path: []string{"project"}}, Right: &ir.PhysicalValue{BindKey: "project"},
		}}},
		{Kind: ir.PhysicalSetOp, Set: set("required_set", "root")},
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &ir.PhysicalPredicateExpression{
			Kind: ir.PhysicalExistsPredicate, Exists: &ir.PhysicalSubplan{Captures: []string{"required_set"}},
		}}},
		{Kind: ir.PhysicalSetOp, Set: set("observation_set", "root")},
		{Kind: ir.PhysicalSetOp, Set: set("specimen_set", "observation_set")},
		{Kind: ir.PhysicalSetOp, Set: set("unused_patient_set", "root")},
		{Kind: ir.PhysicalSetOp, Set: set("unused_research_subject_set", "unused_patient_set")},
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
			Variable: "unused_research_subject_id", Expression: value("unused_research_subject_set"),
		}},
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{
			Variable: "tumor_values",
			Expression: ir.PhysicalExpression{Kind: ir.PhysicalPivotExpression, Cardinality: ir.PhysicalObjectCardinality,
				NullBehavior: ir.PhysicalPreserveNull,
				Pivot: &ir.PhysicalPivotMap{
					Source: ir.PhysicalValue{Variable: "specimen_set"}, ResourceType: "Specimen",
					ColumnsBindKey: "tumor_columns", Correlation: &ir.PhysicalCorrelation{
						Source: ir.PhysicalValue{Variable: "specimen_set"}, KeyResource: "Coding",
					},
				},
			},
		}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{
			{Name: "onset", Value: ir.PhysicalValue{Variable: "root", Path: []string{"payload", "onsetString"}}},
			{Name: "tumor", Expression: &ir.PhysicalExpression{
				Kind: ir.PhysicalObjectLookupExpression, Cardinality: ir.PhysicalScalarCardinality,
				NullBehavior: ir.PhysicalPreserveNull, ObjectLookup: &ir.PhysicalObjectLookup{ObjectVariable: "tumor_values", KeyBindKey: "tumor_key"},
			}},
			{Name: "unselected_route", Value: ir.PhysicalValue{Variable: "unused_research_subject_id"}},
		}}},
	}}
	sequence := &ir.PhysicalStageSequence{
		SourceStageID: "source_projection",
		Stages: []ir.PhysicalConstructionStage{{
			ID: "group", InputStageID: "source_projection", Kind: ir.PhysicalStageGroupOp,
			Group: &ir.PhysicalStageGroup{
				Keys:      []ir.PhysicalStageGroupKey{{InputColumn: "onset"}},
				RowValues: []ir.PhysicalStageRowValue{{InputColumn: "tumor", Policy: "ALL"}},
			},
		}},
	}

	pruneUnusedSourceGroupProjections(&plan, sequence)
	sets, lets, filters := map[string]bool{}, map[string]bool{}, 0
	var returned []ir.PhysicalProjection
	for _, operation := range plan.Operations {
		switch operation.Kind {
		case ir.PhysicalSetOp:
			sets[operation.Set.Variable] = true
		case ir.PhysicalExpressionLetOp:
			lets[operation.ExpressionLet.Variable] = true
		case ir.PhysicalFilterOp:
			filters++
		case ir.PhysicalReturnOp:
			returned = operation.Return.Projections
		}
	}
	for _, variable := range []string{"required_set", "observation_set", "specimen_set"} {
		if !sets[variable] {
			t.Errorf("retained pivot or source constraint lost set %q", variable)
		}
	}
	for _, variable := range []string{"unused_patient_set", "unused_research_subject_set"} {
		if sets[variable] {
			t.Errorf("unselected optional route retained set %q", variable)
		}
	}
	if !lets["tumor_values"] || lets["unused_research_subject_id"] {
		t.Errorf("expression liveness = %#v, want pivot retained and unused route reduction pruned", lets)
	}
	if filters != 2 {
		t.Errorf("source filters = %d, want both project and required-match filters retained", filters)
	}
	if len(returned) != 2 || returned[0].Name != "onset" || returned[1].Name != "tumor" {
		t.Errorf("retained source projections = %#v, want group key and ALL pivot value", returned)
	}
}
