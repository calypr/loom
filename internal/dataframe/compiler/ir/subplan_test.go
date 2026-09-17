package ir

import (
	"strings"
	"testing"
)

func populationSubplanExpression() PhysicalExpression {
	return PhysicalExpression{
		Kind:         PhysicalSubplanExpression,
		Cardinality:  PhysicalArrayCardinality,
		NullBehavior: PhysicalEmptyOnNull,
		Subplan: &PhysicalSubplan{
			Captures: []string{"root"},
			Operations: []PhysicalOperation{{
				Kind:           PhysicalCollectionScanOp,
				CollectionScan: &PhysicalCollectionScan{Variable: "member", CollectionBindKey: "members"},
			}},
			Return: PhysicalExpression{
				Kind: PhysicalValueExpression, Cardinality: PhysicalScalarCardinality, NullBehavior: PhysicalPreserveNull,
				Value: &PhysicalValue{Variable: "member", Path: []string{"id"}},
			},
			Sort:   &PhysicalValue{Variable: "member", Path: []string{"id"}},
			Unique: true,
		},
	}
}

func subplanExpressionPlan(expression PhysicalExpression) PhysicalPlan {
	return PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection": "Specimen",
			"members":         "loom_explorer_selection_members",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalExpressionLetOp, ExpressionLet: &PhysicalExpressionLet{Variable: "members", Expression: expression}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{
				Name: "members", Value: PhysicalValue{Variable: "members"},
			}}}},
		},
	}
}

func TestPhysicalSubplanExpressionRequiresArrayEmptyOnNull(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PhysicalExpression)
		want string
	}{
		{"scalar cardinality", func(expression *PhysicalExpression) { expression.Cardinality = PhysicalScalarCardinality }, "array-valued"},
		{"preserve null", func(expression *PhysicalExpression) { expression.NullBehavior = PhysicalPreserveNull }, "EMPTY_ON_NULL"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expression := populationSubplanExpression()
			test.edit(&expression)
			if err := subplanExpressionPlan(expression).Validate(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestPhysicalSubplanUniqueRequiresStableSort(t *testing.T) {
	expression := populationSubplanExpression()
	expression.Subplan.Sort = nil
	if err := subplanExpressionPlan(expression).Validate(); err == nil || !strings.Contains(err.Error(), "stable sort") {
		t.Fatalf("Validate() = %v, want stable-sort error", err)
	}
}

func TestPhysicalExistsRejectsProjectionOnlySubplanModifiers(t *testing.T) {
	subplan := populationSubplanExpression().Subplan
	plan := PhysicalPlan{
		Version: 1,
		BindVars: map[string]any{
			"root_collection": "Specimen",
			"members":         "loom_explorer_selection_members",
		},
		Operations: []PhysicalOperation{
			{Kind: PhysicalRootScanOp, RootScan: &PhysicalRootScan{Variable: "root", CollectionBindKey: "root_collection"}},
			{Kind: PhysicalFilterOp, Filter: &PhysicalFilter{Expression: &PhysicalPredicateExpression{
				Kind: PhysicalExistsPredicate, Exists: subplan,
			}}},
			{Kind: PhysicalReturnOp, Return: &PhysicalReturn{Projections: []PhysicalProjection{{
				Name: "key", Value: PhysicalValue{Variable: "root", Path: []string{"_key"}},
			}}}},
		},
	}
	if err := plan.Validate(); err == nil || !strings.Contains(err.Error(), "projection-only") {
		t.Fatalf("Validate() = %v, want projection-only modifier error", err)
	}
}
