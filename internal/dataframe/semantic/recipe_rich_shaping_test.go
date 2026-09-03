package semantic

import (
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/spec"
)

func TestLowerRecipePivotsUsesGeneratedFamilyAndDeclaredColumns(t *testing.T) {
	scope := newRootScope("Condition")
	pivots, err := lowerRecipePivots("Condition", "root", scope, []recipe.Pivot{{
		Name:       "diagnosis",
		ColumnExpr: recipe.Expression{Select: "code.coding[].display"},
		ValueExpr:  recipe.Expression{Select: "code.text"},
		Columns:    []string{"melanoma", "glioma"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pivots) != 1 {
		t.Fatalf("pivots = %#v", pivots)
	}
	got := pivots[0]
	if got.Family == "" || got.ColumnSelector.CanonicalPath() != "code.coding[].display" || got.ValueSelector.CanonicalPath() != "code.text" {
		t.Fatalf("unexpected pivot = %#v", got)
	}
	if got.Columns[0] != "melanoma" || got.Columns[1] != "glioma" {
		t.Fatalf("declared column order changed: %#v", got.Columns)
	}
}

func TestLowerRecipePivotsRejectsUnsupportedPairAndDuplicateColumn(t *testing.T) {
	scope := newRootScope("Patient")
	_, err := lowerRecipePivots("Patient", "root", scope, []recipe.Pivot{{
		Name:       "bad",
		ColumnExpr: recipe.Expression{Select: "gender"},
		ValueExpr:  recipe.Expression{Select: "id"},
		Columns:    []string{"x", "x"},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("expected duplicate-column error, got %v", err)
	}
	_, err = lowerRecipePivots("Patient", "root", scope, []recipe.Pivot{{
		Name:       "bad",
		ColumnExpr: recipe.Expression{Select: "gender"},
		ValueExpr:  recipe.Expression{Select: "id"},
		Columns:    []string{"x"},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported pivot selector pair") {
		t.Fatalf("expected generated pivot-family error, got %v", err)
	}
}

func TestLowerRecipeAggregatesMapsSelectorWhereAndOperation(t *testing.T) {
	scope := newRootScope("Patient")
	want := "female"
	aggregates, err := lowerRecipeAggregates("Patient", "root", scope, []recipe.Aggregate{{
		Name:      "female_count",
		Operation: recipe.AggregateCountDistinct,
		Expr:      recipeExpr("gender"),
		Where: &recipe.Filter{
			Select:   "gender",
			Operator: recipe.FilterEquals,
			Values:   []recipe.FilterValue{{Kind: recipe.FilterString, String: &want}},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].Selector == nil || aggregates[0].Predicate == nil {
		t.Fatalf("aggregate selectors missing: %#v", aggregates)
	}
	if aggregates[0].Operation != string(recipe.AggregateCountDistinct) || aggregates[0].PredicateEquals != want {
		t.Fatalf("aggregate = %#v", aggregates[0])
	}
}

func TestLowerRecipeAggregatesRejectsUnrepresentableWhereAndCountExpr(t *testing.T) {
	scope := newRootScope("Patient")
	_, err := lowerRecipeAggregates("Patient", "root", scope, []recipe.Aggregate{{
		Name: "count", Operation: recipe.AggregateCount, Expr: recipeExpr("id"),
	}})
	if err == nil || !strings.Contains(err.Error(), "not accepted") {
		t.Fatalf("expected count expression rejection, got %v", err)
	}
	_, err = lowerRecipeAggregates("Patient", "root", scope, []recipe.Aggregate{{
		Name: "count", Operation: recipe.AggregateCount, Where: &recipe.Filter{
			Select: "gender", Operator: recipe.FilterNotEquals,
			Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: stringPtr("female")}},
		},
	}})
	if err == nil || !strings.Contains(err.Error(), "not representable") {
		t.Fatalf("expected predicate-shape rejection, got %v", err)
	}
}

func TestLowerRecipeAggregatesPreservesOutputNameAndRequiredValues(t *testing.T) {
	scope := newRootScope("Specimen")
	aggregates, err := lowerRecipeAggregates("Specimen", "specimen", scope, []recipe.Aggregate{{
		Name: "paired", OutputName: "has_pair", Operation: recipe.AggregateContainsAll,
		Expr: recipeExpr("type.coding[].code"), RequiredValues: []string{"Tumor", "Normal"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].OutputName != "has_pair" || aggregates[0].ValueKind != "boolean" || len(aggregates[0].RequiredValues) != 2 {
		t.Fatalf("aggregate = %#v", aggregates)
	}
}

func TestLowerRecipeAggregatesPreservesCodePredicateKind(t *testing.T) {
	code := "Tumor"
	scope := newRootScope("Specimen")
	aggregates, err := lowerRecipeAggregates("Specimen", "specimen", scope, []recipe.Aggregate{{
		Name: "tumor_count", Operation: recipe.AggregateCount,
		Where: &recipe.Filter{Select: "specimen.type.coding[].code", Operator: recipe.FilterEquals, Quantifier: recipe.QuantifierAny, Values: []recipe.FilterValue{{Kind: recipe.FilterCode, Code: &recipe.CodeValue{Code: code}}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregates) != 1 || aggregates[0].PredicateKind != spec.FilterCode || aggregates[0].PredicateEquals != code {
		t.Fatalf("aggregate = %#v", aggregates)
	}
}

func TestLowerRecipeSlicesUsesProjectionFallbacksAndPredicate(t *testing.T) {
	scope := newRootScope("Patient")
	want := "female"
	slices, err := lowerRecipeSlices("Patient", "root", scope, []recipe.RepresentativeSlice{{
		Name: "representatives", Limit: 3,
		Where: &recipe.Filter{Select: "gender", Operator: recipe.FilterEquals, Values: []recipe.FilterValue{{Kind: recipe.FilterString, String: &want}}},
		Fields: []recipe.Field{{
			Name: "identifier", Expr: *recipeExpr("id"),
			Fallbacks: []recipe.Expression{*recipeExpr("gender")}, ValueMode: recipe.ValueModeFirst,
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(slices) != 1 || len(slices[0].Fields) != 1 || slices[0].Predicate == nil || slices[0].PredicateEquals != want {
		t.Fatalf("slice = %#v", slices)
	}
	field := slices[0].Fields[0]
	if len(field.Fallbacks) != 1 || field.Projection != spec.ProjectionFirst {
		t.Fatalf("slice field = %#v", field)
	}
}

func TestLowerRecipeSlicesRejectsExpressionFieldsAndForeignAlias(t *testing.T) {
	scope := newRootScope("Patient")
	_, err := lowerRecipeSlices("Patient", "root", scope, []recipe.RepresentativeSlice{{
		Name: "representatives", Limit: 1,
		Fields: []recipe.Field{{Name: "x", Expr: recipe.Expression{Call: "first", Args: []recipe.Expression{*recipeExpr("id")}}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "must be a selector") {
		t.Fatalf("expected selector-only slice error, got %v", err)
	}
	_, err = lowerRecipeSlices("Patient", "root", scope, []recipe.RepresentativeSlice{{
		Name: "representatives", Limit: 1,
		Fields: []recipe.Field{{Name: "x", Expr: recipe.Expression{Select: "other.id"}}},
	}})
	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("expected foreign-context error, got %v", err)
	}
}

func TestLowerRecipeRichShapingCoordinatesFamilies(t *testing.T) {
	scope := newRootScope("Condition")
	pivots, aggregates, slices, err := lowerRecipeRichShaping("Condition", "root", scope,
		[]recipe.Pivot{{Name: "diagnosis", ColumnExpr: recipe.Expression{Select: "code.coding[].display"}, ValueExpr: recipe.Expression{Select: "code.text"}, Columns: []string{"x"}}},
		[]recipe.Aggregate{{Name: "count", Operation: recipe.AggregateCount}},
		[]recipe.RepresentativeSlice{{Name: "rows", Limit: 1, Fields: []recipe.Field{{Name: "id", Expr: *recipeExpr("id")}}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(pivots) != 1 || len(aggregates) != 1 || len(slices) != 1 {
		t.Fatalf("rich shaping = %#v %#v %#v", pivots, aggregates, slices)
	}
}

func recipeExpr(selectPath string) *recipe.Expression {
	expr := recipe.Expression{Select: selectPath}
	return &expr
}

func stringPtr(value string) *string { return &value }
