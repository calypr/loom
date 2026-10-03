package lower

import (
	"reflect"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestRootIDFilterUsesIndexedDocumentIdentityBeforeExpansion(t *testing.T) {
	for _, operator := range []recipe.FilterOperator{recipe.FilterEquals, recipe.FilterIn} {
		t.Run(string(operator), func(t *testing.T) {
			values := []recipe.FilterValue{{Kind: recipe.FilterString, String: stringPointer("selected-observation")}}
			if operator == recipe.FilterIn {
				values = append(values, recipe.FilterValue{Kind: recipe.FilterString, String: stringPointer("another-observation")})
			}
			output := compileExpansionRecipeOutput(t, recipe.Output{
				Name: "ScopedCodings", RootResourceType: "Observation", RootOccurrenceID: "observation-root", RowGrain: "expanded",
				Fields:   []recipe.Field{{Name: "observation_id", Expr: recipe.Expression{Select: "root.id"}}},
				Filters:  []recipe.Filter{{Select: "root.id", Operator: operator, Values: values}},
				Expand:   &recipe.Expansion{OwnerOccurrenceID: "observation-root", From: recipe.Expression{Select: "root.component[].code.coding[]"}, As: "item", EmptyPolicy: recipe.ExpansionExclude},
				Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			})
			indexedAt, expandedAt, retainedPredicate := -1, -1, false
			for index, operation := range output.Plan.Operations {
				if operation.Kind == ir.PhysicalUnnestOp {
					expandedAt = index
				}
				if operation.Filter == nil {
					continue
				}
				if operation.Filter.Expression != nil {
					comparison := operation.Filter.Expression.Comparison
					if comparison != nil && comparison.Operator == string(operator) && comparison.LeftExpression != nil && comparison.LeftExpression.Extract != nil && comparison.LeftExpression.Extract.Selector.CanonicalPath() == "id" && comparison.LeftExpression.Extract.Source.Variable == "root" && reflect.DeepEqual(comparison.LeftExpression.Extract.Source.Path, []string{"payload"}) {
						retainedPredicate = true
					}
				}
				predicate := operation.Filter.Predicate
				if operation.Filter.Expression == nil && predicate.Operator == string(operator) && predicate.Left.Variable == "root" && reflect.DeepEqual(predicate.Left.Path, []string{"id"}) && predicate.Right != nil && predicate.Right.BindKey != "" {
					indexedAt = index
				}
			}
			if indexedAt < 0 || expandedAt < 0 || indexedAt >= expandedAt {
				t.Fatalf("source-ID filter has no indexed document predicate before expansion: indexed=%d expansion=%d", indexedAt, expandedAt)
			}
			if !retainedPredicate {
				t.Fatal("indexed narrowing removed the original FHIR value predicate")
			}
		})
	}
}

func TestRootIDNarrowingDoesNotChangeOtherFields(t *testing.T) {
	for _, filter := range []recipe.Filter{
		{Select: "root.component[].valueString", Operator: recipe.FilterEquals, Quantifier: recipe.QuantifierAny, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: stringPointer("selected-value")}}},
		{Select: "root.identifier[].value", Operator: recipe.FilterIn, Quantifier: recipe.QuantifierAny, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: stringPointer("selected-identifier")}}},
	} {
		t.Run(filter.Select+string(filter.Quantifier), func(t *testing.T) {
			output := compileExpansionRecipeOutput(t, recipe.Output{
				Name: "ScopedCodings", RootResourceType: "Observation", RootOccurrenceID: "observation-root", RowGrain: "expanded",
				Fields:   []recipe.Field{{Name: "observation_id", Expr: recipe.Expression{Select: "root.id"}}},
				Filters:  []recipe.Filter{filter},
				Expand:   &recipe.Expansion{OwnerOccurrenceID: "observation-root", From: recipe.Expression{Select: "root.component[].code.coding[]"}, As: "item", EmptyPolicy: recipe.ExpansionExclude},
				Identity: &recipe.Identity{Name: "row", Expansion: &recipe.ExpansionIdentity{}},
			})
			for _, operation := range output.Plan.Operations {
				if operation.Filter == nil || operation.Filter.Expression != nil {
					continue
				}
				predicate := operation.Filter.Predicate
				if predicate.Left.Variable == "root" && reflect.DeepEqual(predicate.Left.Path, []string{"id"}) {
					t.Fatal("unrelated field filter gained a positive document-ID restriction")
				}
			}
		})
	}
}
