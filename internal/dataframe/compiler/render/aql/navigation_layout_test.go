package aql

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
)

func TestRootPageRelatedSetSegmentRejectsNonRootCapture(t *testing.T) {
	operations := make([]ir.PhysicalOperation, 11)
	operations[5] = ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "GT",
			Left:     ir.PhysicalValue{Variable: "root", Path: []string{"_key"}},
			Right:    &ir.PhysicalValue{BindKey: "loom_root_page_after_key"},
		}},
	}
	operations[6] = ir.PhysicalOperation{
		Kind: ir.PhysicalSetOp,
		Set: &ir.PhysicalSet{
			Variable:  "child_set_1",
			Unique:    true,
			SortByKey: true,
			Subplan: ir.PhysicalSubplan{
				Captures: []string{"other_root"},
				Operations: []ir.PhysicalOperation{{
					Kind:      ir.PhysicalTraversalOp,
					Traversal: &ir.PhysicalTraversal{SourceVariable: "other_root"},
				}},
			},
			Reduction: &ir.PhysicalSetReduction{
				Variable:          "child_set_1_reduced",
				SourceSetVariable: "child_set_1",
				Fields: []ir.PhysicalSetReductionField{{
					Name: "__loom_reduced_0",
					Mode: ir.PhysicalSetReductionFirst,
				}},
			},
		},
	}
	operations[7] = ir.PhysicalOperation{
		Kind: ir.PhysicalFilterOp,
		Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQ",
			Left:     ir.PhysicalValue{Variable: "child_set_1_reduced", Path: []string{"__loom_reduced_0"}},
			Right:    &ir.PhysicalValue{BindKey: "construction_filter_value"},
		}},
	}
	operations[8] = ir.PhysicalOperation{Kind: ir.PhysicalSortOp}
	operations[9] = ir.PhysicalOperation{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind}}
	operations[10] = ir.PhysicalOperation{Kind: ir.PhysicalReturnOp}

	if err := validateRootPageRelatedSet(operations[6], "root"); err == nil {
		t.Fatal("non-root correlated set was accepted in the root-page predicate segment")
	}
}

func TestRootPageRelatedSetSegmentRejectsUnnestBarrier(t *testing.T) {
	rootValue := func(path ...string) ir.PhysicalValue {
		return ir.PhysicalValue{Variable: "root", Path: path}
	}
	equality := func(left, right ir.PhysicalValue) ir.PhysicalOperation {
		return ir.PhysicalOperation{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "EQUALS", Left: left, Right: &right,
		}}}
	}

	itemSource := rootValue("payload", "items")
	unnest := ir.PhysicalUnnest{
		Owner:           ir.PhysicalUnnestOwner{RootVariable: "root", OwnerVariable: "root"},
		OutputVariable:  "items",
		Ordinality:      "item_index",
		HasItemVariable: "has_item",
		Expression: ir.PhysicalExpression{
			Kind: ir.PhysicalValueExpression, Cardinality: ir.PhysicalArrayCardinality,
			NullBehavior: ir.PhysicalEmptyOnNull, Value: &itemSource,
		},
		EmptyPolicy: ir.PhysicalUnnestExclude,
	}
	set := &ir.PhysicalSet{
		Variable: "child_set_1", Unique: true, SortByKey: true,
		Subplan: ir.PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalTraversalOp, Traversal: &ir.PhysicalTraversal{
				SourceVariable: "root", TargetVariable: "patient", EdgeVariable: "edge",
			}}},
		},
		Reduction: &ir.PhysicalSetReduction{
			Variable: "child_set_1_reduced", SourceSetVariable: "child_set_1",
			Fields: []ir.PhysicalSetReductionField{{Name: "__loom_reduced_0", SourceField: "id", Mode: ir.PhysicalSetReductionFirst}},
		},
	}
	operations := []ir.PhysicalOperation{
		{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
		equality(rootValue("project"), ir.PhysicalValue{BindKey: "project"}),
		equality(rootValue("dataset_generation"), ir.PhysicalValue{BindKey: datasetGenerationBindKey}),
		{Kind: ir.PhysicalDerivedLetOp, DerivedLet: &ir.PhysicalDerivedLet{
			Variable: "root_scope_allowed", Operator: "AUTH_RESOURCE_PATH_ALLOWED",
			Inputs: []ir.PhysicalValue{
				rootValue("auth_resource_path"), {BindKey: "auth_resource_paths"}, {BindKey: "auth_resource_paths_unrestricted"},
			},
		}},
		equality(ir.PhysicalValue{Variable: "root_scope_allowed"}, ir.PhysicalValue{BindKey: "scope_allowed"}),
		{Kind: ir.PhysicalFilterOp, Filter: &ir.PhysicalFilter{Predicate: ir.PhysicalPredicate{
			Operator: "GT", Left: rootValue("_key"), Right: &ir.PhysicalValue{BindKey: "loom_root_page_after_key"},
		}}},
		{Kind: ir.PhysicalSetOp, Set: set},
		equality(ir.PhysicalValue{Variable: "child_set_1_reduced", Path: []string{"__loom_reduced_0"}}, ir.PhysicalValue{BindKey: "construction_filter_value"}),
		{Kind: ir.PhysicalUnnestOp, Unnest: &unnest},
		{Kind: ir.PhysicalSortOp, Sort: &ir.PhysicalSort{Keys: ir.PhysicalUnnestSortKeys(unnest)}},
		{Kind: ir.PhysicalLimitOp, Limit: &ir.PhysicalLimit{BindKey: genericPhysicalExecutionLimitBind}},
		{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{Projections: []ir.PhysicalProjection{{Name: "root_id", Value: rootValue("id")}}}},
	}
	plan := ir.PhysicalPlan{Version: 1, Operations: operations}

	if _, err := buildNavigationRenderLayout(plan); err == nil || !strings.Contains(err.Error(), "UNNEST") {
		t.Fatalf("root-page related SET segment error = %v, want UNNEST barrier rejection", err)
	}
}
