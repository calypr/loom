package compiler

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestProjectionShardsBoundPublicColumnsAndPruneUnusedFamilies(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	returnIndex := len(plan.Operations) - 1
	if plan.Operations[returnIndex].Kind != ir.PhysicalReturnOp {
		t.Fatalf("last operation = %s, want RETURN", plan.Operations[returnIndex].Kind)
	}
	plan.Operations = append(plan.Operations[:returnIndex], append([]ir.PhysicalOperation{
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "__loom_family_used", Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}},
		{Kind: ir.PhysicalExpressionLetOp, ExpressionLet: &ir.PhysicalExpressionLet{Variable: "__loom_family_unused", Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}},
	}, plan.Operations[returnIndex:]...)...)
	returnIndex = len(plan.Operations) - 1
	runtimeProjection := ir.PhysicalProjection{Name: "__loom_dynamic_runtime_keys", Hidden: true, Expression: &ir.PhysicalExpression{
		Kind: ir.PhysicalObjectExpression, Cardinality: ir.PhysicalObjectCardinality, NullBehavior: ir.PhysicalPreserveNull,
		Object: &ir.PhysicalObject{Fields: []ir.PhysicalExpressionProjection{
			{Name: "family_a", Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}},
			{Name: "family_b", Expression: ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}},
		}},
	}}
	projections := []ir.PhysicalProjection{
		{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
		{Name: "__loom_row_id", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}},
		{Name: "family_value", Expression: &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "__loom_family_used"}}},
		runtimeProjection,
	}
	schema := []lower.CompiledOutputColumn{{Name: "family_value"}}
	for index := 0; index < DefaultProjectionShardPolicy().MaxPublicProjections+3; index++ {
		name := fmt.Sprintf("column_%03d", index)
		projections = append(projections, ir.PhysicalProjection{Name: name, Expression: &ir.PhysicalExpression{Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalScalarCardinality, NullBehavior: ir.PhysicalPreserveNull, Value: &ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}})
		schema = append(schema, lower.CompiledOutputColumn{Name: name})
	}
	plan.Operations[returnIndex].Return = &ir.PhysicalReturn{Projections: projections}
	lastDynamicColumn := fmt.Sprintf("column_%03d", DefaultProjectionShardPolicy().MaxPublicProjections+2)
	output := lower.CompiledRecipeOutput{Name: "wide", OutputSchema: schema, DynamicColumns: []lower.DynamicColumnMetadata{{Name: "family_value", DynamicName: "family_a"}, {Name: lastDynamicColumn, DynamicName: "family_b"}}, Plan: plan}

	windowed, err := withGenericPhysicalExecutionWindow(plan, 0)
	if err != nil {
		t.Fatal(err)
	}
	plans, names, err := projectionShardPhysicalPlans(windowed, output, DefaultProjectionShardPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) < 2 {
		t.Fatalf("got %d projection shards, want more than one", len(plans))
	}
	seen := make(map[string]int)
	for index, shard := range plans {
		if err := shard.Validate(); err != nil {
			t.Fatalf("shard %d validation: %v", index+1, err)
		}
		rendered, err := aql.RenderPhysicalPlan(shard)
		if err != nil {
			t.Fatalf("shard %d render: %v", index+1, err)
		}
		if strings.Contains(rendered.Query, "__loom_family_unused") {
			t.Fatalf("shard %d retained unused expression family:\n%s", index+1, rendered.Query)
		}
		wantRuntimeFamily := false
		for _, name := range names[index] {
			wantRuntimeFamily = wantRuntimeFamily || name == "family_value" || name == lastDynamicColumn
		}
		runtime, ok := findPhysicalProjection(shard.Operations[len(shard.Operations)-1].Return.Projections, "__loom_dynamic_runtime_keys")
		if wantRuntimeFamily && (!ok || runtime.Expression == nil || runtime.Expression.Object == nil || len(runtime.Expression.Object.Fields) != 1) {
			t.Fatalf("shard %d dynamic runtime-key projection = %#v, want one family", index+1, runtime)
		}
		if !wantRuntimeFamily && ok {
			t.Fatalf("shard %d retained an unrelated dynamic runtime-key projection", index+1)
		}
		for _, name := range names[index] {
			seen[name]++
		}
	}
	if len(seen) != len(schema) {
		t.Fatalf("sharded public columns = %d, want %d", len(seen), len(schema))
	}
	for _, column := range schema {
		if seen[column.Name] != 1 {
			t.Errorf("public column %q appears %d times across shards", column.Name, seen[column.Name])
		}
	}
}

func TestProjectionShardsRejectWideTableReshapeButKeepNarrow(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	returnIndex := len(plan.Operations) - 1
	plan.Operations = append(plan.Operations[:returnIndex], ir.PhysicalOperation{Kind: ir.PhysicalGroupedPivotOp, GroupedPivot: &ir.PhysicalGroupedPivot{OutputRowVariable: "reshaped"}}, plan.Operations[returnIndex])
	plan.Operations[len(plan.Operations)-1].Return = &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "one", Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}}
	output := lower.CompiledRecipeOutput{Name: "reshape", OutputSchema: []lower.CompiledOutputColumn{{Name: "one"}}, Plan: plan}
	plans, names, err := projectionShardPhysicalPlans(plan, output, DefaultProjectionShardPolicy())
	if err != nil {
		t.Fatalf("narrow reshape rejected: %v", err)
	}
	if len(plans) != 1 || len(names) != 1 || !reflect.DeepEqual(plans[0], plan) {
		t.Fatalf("narrow reshape was changed by projection sharding: plans=%d names=%d", len(plans), len(names))
	}
}

func TestProjectionShardsRejectWideUnpivotPrecisely(t *testing.T) {
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	returnIndex := len(plan.Operations) - 1
	plan.Operations = append(plan.Operations[:returnIndex], ir.PhysicalOperation{Kind: ir.PhysicalUnpivotOp, Unpivot: &ir.PhysicalUnpivot{OutputRowVariable: "reshaped"}}, plan.Operations[returnIndex])
	projections := []ir.PhysicalProjection{{Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}}}
	schema := make([]lower.CompiledOutputColumn, 0, DefaultProjectionShardPolicy().MaxPublicProjections+1)
	for index := 0; index < DefaultProjectionShardPolicy().MaxPublicProjections+1; index++ {
		name := fmt.Sprintf("column_%03d", index)
		projections = append(projections, ir.PhysicalProjection{Name: name, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}}})
		schema = append(schema, lower.CompiledOutputColumn{Name: name})
	}
	plan.Operations[len(plan.Operations)-1].Return = &ir.PhysicalReturn{Projections: projections}
	output := lower.CompiledRecipeOutput{Name: "unpivot", OutputSchema: schema, Plan: plan}
	if _, _, err := projectionShardPhysicalPlans(plan, output, DefaultProjectionShardPolicy()); err == nil || !strings.Contains(err.Error(), "unpivot outputs are unsupported") {
		t.Fatalf("wide unpivot error = %v, want precise unsupported error", err)
	}
}

func TestProjectionShardPolicyIsNamedAndBounded(t *testing.T) {
	policy := DefaultProjectionShardPolicy()
	if policy.Name == "" || policy.MaxPublicProjections < 1 {
		t.Fatalf("invalid projection shard policy: %#v", policy)
	}
}

func TestProjectionDependencyClosureKeepsSelectedSetAndAncestors(t *testing.T) {
	plan := projectionDependencyTestPlan(t)
	returnIndex := len(plan.Operations) - 1
	plan.Operations[returnIndex].Return = &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
		Name: "selected", Expression: &ir.PhysicalExpression{
			Kind: ir.PhysicalAggregateExpression, Cardinality: ir.PhysicalScalarCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull,
			Aggregate:    &ir.PhysicalAggregate{Source: ir.PhysicalValue{Variable: "child_set"}, Operation: ir.PhysicalCountAggregate},
		},
	}}}
	pruned, err := pruneProjectionDependencies(plan, returnIndex)
	if err != nil {
		t.Fatal(err)
	}
	if err := pruned.Validate(); err != nil {
		t.Fatalf("pruned plan does not validate: %v", err)
	}
	if !hasPhysicalSet(pruned, "parent_set") || !hasPhysicalSet(pruned, "child_set") {
		t.Fatalf("selected set dependency chain was pruned: %#v", pruned.Operations)
	}
	if hasPhysicalSet(pruned, "unrelated_set") {
		t.Fatalf("unrelated set survived dependency pruning: %#v", pruned.Operations)
	}
	if want := withoutPhysicalSets(plan, "unrelated_set"); !reflect.DeepEqual(pruned.Operations, want) {
		t.Fatalf("dependency pruning changed row semantics: got %v, want %v", projectionTestOperationNames(pruned.Operations), projectionTestOperationNames(want))
	}
	rendered, err := aql.RenderPhysicalPlan(pruned)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered.Query, "unrelated_set") {
		t.Fatalf("rendered plan retained unrelated set:\n%s", rendered.Query)
	}
}

func TestProjectionDependencyClosureKeepsFilterExistsAndUnnestSets(t *testing.T) {
	tests := []struct {
		name string
		add  func([]ir.PhysicalOperation, int) []ir.PhysicalOperation
		want string
	}{
		{
			name: "filter",
			add: func(operations []ir.PhysicalOperation, returnIndex int) []ir.PhysicalOperation {
				filter := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{Operator: "EQUALS", Left: ir.PhysicalValue{Variable: "filter_set"}, Right: &ir.PhysicalValue{BindKey: "filter_value"}}}}
				return insertProjectionTestOperations(operations, returnIndex, filter)
			},
			want: "filter_set",
		},
		{
			name: "exists",
			add: func(operations []ir.PhysicalOperation, returnIndex int) []ir.PhysicalOperation {
				exists := ir.PhysicalPredicateExpression{
					Kind: ir.PhysicalExistsPredicate,
					Exists: &ir.PhysicalSubplan{
						Captures: []string{"exists_set"},
						Return:   physicalValueExpression(ir.PhysicalValue{Variable: "exists_set"}, ir.PhysicalScalarCardinality),
					},
				}
				filter := ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Expression: &exists}}
				return insertProjectionTestOperations(operations, returnIndex, filter)
			},
			want: "exists_set",
		},
		{
			name: "unnest",
			add: func(operations []ir.PhysicalOperation, returnIndex int) []ir.PhysicalOperation {
				unnest := ir.PhysicalOperation{Kind: ir.PhysicalUnnestOp, Unnest: &ir.PhysicalUnnest{
					Owner:          ir.PhysicalUnnestOwner{OccurrenceID: "root", ResourceType: "Patient", RootVariable: "root", OwnerVariable: "root"},
					OutputVariable: "item", HasItemVariable: "has_item",
					Expression:  physicalValueExpression(ir.PhysicalValue{Variable: "unnest_set"}, ir.PhysicalArrayCardinality),
					EmptyPolicy: ir.PhysicalUnnestExclude,
				}}
				return insertProjectionTestOperations(operations, returnIndex, unnest)
			},
			want: "unnest_set",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := projectionDependencyTestPlan(t)
			returnIndex := len(plan.Operations) - 1
			plan.BindVars["filter_value"] = "present"
			plan.Operations = insertProjectionTestOperations(plan.Operations, returnIndex, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("filter_set", "")})
			returnIndex = len(plan.Operations) - 1
			plan.Operations = insertProjectionTestOperations(plan.Operations, returnIndex, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("exists_set", "")})
			returnIndex = len(plan.Operations) - 1
			plan.Operations = insertProjectionTestOperations(plan.Operations, returnIndex, ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("unnest_set", "")})
			returnIndex = len(plan.Operations) - 1
			plan.Operations = test.add(plan.Operations, returnIndex)
			returnIndex = len(plan.Operations) - 1
			pruned, err := pruneProjectionDependencies(plan, returnIndex)
			if err != nil {
				t.Fatal(err)
			}
			if !hasPhysicalSet(pruned, test.want) {
				t.Fatalf("semantic %s set was pruned: %#v", test.name, pruned.Operations)
			}
			if hasPhysicalSet(pruned, "unrelated_set") {
				t.Fatalf("unrelated set survived semantic %s pruning", test.name)
			}
			var omit []string
			for _, variable := range []string{"parent_set", "child_set", "unrelated_set", "filter_set", "exists_set", "unnest_set"} {
				if variable != test.want {
					omit = append(omit, variable)
				}
			}
			if want := withoutPhysicalSets(plan, omit...); !reflect.DeepEqual(pruned.Operations, want) {
				t.Fatalf("semantic %s pruning changed row semantics: got %v, want %v", test.name, projectionTestOperationNames(pruned.Operations), projectionTestOperationNames(want))
			}
		})
	}
}

func projectionDependencyTestPlan(t *testing.T) ir.PhysicalPlan {
	t.Helper()
	plan, err := buildGenericPhysicalPlan(semantic.OutputPlan{Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient"}})
	if err != nil {
		t.Fatal(err)
	}
	returnIndex := len(plan.Operations) - 1
	plan.Operations = append(plan.Operations[:returnIndex],
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("parent_set", "")},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("child_set", "parent_set")},
		ir.PhysicalOperation{Kind: ir.PhysicalSetOp, Set: projectionTestSet("unrelated_set", "")},
		plan.Operations[returnIndex])
	plan.Operations[len(plan.Operations)-1].Return = &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{
		Name: "_key", Hidden: true, Value: ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
	}}}
	plan.BindVars["edge_collection"] = "fhir_edge"
	for _, variable := range []string{"parent_set", "unrelated_set", "filter_set", "exists_set", "unnest_set"} {
		plan.BindVars["edge_label_"+variable] = "subject_Patient"
		plan.BindVars["type_"+variable] = "Patient"
	}
	if err := plan.Validate(); err != nil {
		t.Fatalf("projection dependency fixture does not validate: %v", err)
	}
	return plan
}

func withoutPhysicalSets(plan ir.PhysicalPlan, variables ...string) []ir.PhysicalOperation {
	omit := make(map[string]struct{}, len(variables))
	for _, variable := range variables {
		omit[variable] = struct{}{}
	}
	operations := make([]ir.PhysicalOperation, 0, len(plan.Operations))
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil {
			if _, ok := omit[operation.Set.Variable]; ok {
				continue
			}
		}
		operations = append(operations, operation)
	}
	return operations
}

func projectionTestOperationNames(operations []ir.PhysicalOperation) []string {
	names := make([]string, 0, len(operations))
	for _, operation := range operations {
		name := string(operation.Kind)
		if operation.Set != nil {
			name += "(" + operation.Set.Variable + ")"
		}
		names = append(names, name)
	}
	return names
}

func insertProjectionTestOperations(operations []ir.PhysicalOperation, index int, inserted ...ir.PhysicalOperation) []ir.PhysicalOperation {
	result := make([]ir.PhysicalOperation, 0, len(operations)+len(inserted))
	result = append(result, operations[:index]...)
	result = append(result, inserted...)
	result = append(result, operations[index:]...)
	return result
}

func projectionTestSet(variable, source string) *ir.PhysicalSet {
	if source != "" {
		return &ir.PhysicalSet{
			Variable: variable, SourceSetVariable: source, ItemVariable: variable + "_item",
			Subplan: ir.PhysicalSubplan{
				Captures: []string{source},
				Return:   physicalValueExpression(ir.PhysicalValue{Variable: variable + "_item"}, ir.PhysicalObjectCardinality),
			},
		}
	}
	traversal := ir.PhysicalTraversal{
		SourceVariable: "root", TargetVariable: variable + "_node", EdgeVariable: variable + "_edge",
		Direction: ir.PhysicalInbound, EdgeCollectionBindKey: "edge_collection",
		EdgeLabelBindKey: "edge_label_" + variable, TargetTypeBindKey: "type_" + variable,
		EdgeTargetTypeField: "from_type",
	}
	return &ir.PhysicalSet{
		Variable: variable,
		Subplan: ir.PhysicalSubplan{
			Captures:   []string{"root"},
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &traversal}},
			Return:     physicalValueExpression(ir.PhysicalValue{Variable: traversal.TargetVariable}, ir.PhysicalObjectCardinality),
		},
	}
}

func hasPhysicalSet(plan ir.PhysicalPlan, variable string) bool {
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalSetOp && operation.Set != nil && operation.Set.Variable == variable {
			return true
		}
	}
	return false
}
