package lower

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/unit"
)

func TestCompileResolvedRecipePlanLowersTypedDerivedColumnsAfterTransforms(t *testing.T) {
	output := derivedTestOutput()
	basePlan := compileDerivedTestOutput(t, output)

	compiledOutput := compileDerivedTestOutput(t, outputWithDerivedColumns())
	if got, want := returnProjectionForTest(t, compiledOutput.Plan, "whole"), returnProjectionForTest(t, basePlan.Plan, "whole"); !reflect.DeepEqual(got, want) {
		t.Fatalf("derived compilation changed the existing whole projection: got %#v, want %#v", got, want)
	}
	if got := countExpressionLets(compiledOutput.Plan); got != 8 {
		t.Fatalf("expression LET count = %d, want 8 (two inputs and six derived values)", got)
	}
	for _, test := range []struct {
		name        string
		kind        expression.ValueKind
		cardinality expression.Cardinality
		nullable    bool
	}{
		{"integer_sum", expression.KindInteger, expression.OptionalOne, true},
		{"decimal_sum", expression.KindDecimal, expression.RequiredOne, false},
		{"ratio_null", expression.KindDecimal, expression.OptionalOne, true},
		{"ratio_error", expression.KindDecimal, expression.RequiredOne, false},
		{"scale", expression.KindDecimal, expression.RequiredOne, false},
		{"difference", expression.KindInteger, expression.RequiredOne, false},
	} {
		column, found := outputSchemaColumn(compiledOutput.OutputSchema, test.name)
		if !found {
			t.Fatalf("output schema is missing derived column %q: %#v", test.name, compiledOutput.OutputSchema)
		}
		if column.Kind != string(test.kind) || column.Cardinality != string(test.cardinality) || column.Nullable != test.nullable {
			t.Fatalf("schema for %q = %#v, want kind=%s cardinality=%s nullable=%t", test.name, column, test.kind, test.cardinality, test.nullable)
		}
		if column.SemanticPath != "derived:calc_"+test.name {
			t.Fatalf("schema provenance for %q = %q", test.name, column.SemanticPath)
		}
	}

	lastLet, returnIndex := -1, -1
	for index, operation := range compiledOutput.Plan.Operations {
		if operation.Kind == ir.PhysicalExpressionLetOp {
			lastLet = index
		}
		if operation.Kind == ir.PhysicalReturnOp {
			returnIndex = index
		}
	}
	if lastLet < 0 || returnIndex <= lastLet {
		t.Fatalf("derived LETs are not immediately in the final row scope before RETURN: lastLET=%d RETURN=%d", lastLet, returnIndex)
	}

	rendered, err := aql.RenderPhysicalPlan(compiledOutput.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{" + ", " - ", " * ", " / ", "ASSERT("} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered query is missing %q: %s", expected, rendered.Query)
		}
	}
	if !derivedBindValueContains(rendered.BindVars, "DERIVED_MISSING_INPUT") || !derivedBindValueContains(rendered.BindVars, "DERIVED_DIVISION_BY_ZERO") {
		t.Fatalf("rendered policy errors are missing from binds: %#v", rendered.BindVars)
	}
	if value, exists := rendered.BindVars["derived_literal_1"]; !exists || value != int64(1) {
		t.Fatalf("integer literal bind = %#v, exists=%t", value, exists)
	}
}

func TestCompileResolvedRecipePlanRejectsNonNumericAndUnknownDerivedInputs(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  string
	}{
		{"non-numeric", "text", "must be integer or decimal"},
		{"unknown", "missing", "unknown public output column"},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := recipe.Output{
				Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
				Fields: []recipe.Field{{Name: "text", Expr: recipe.Expression{Literal: json.RawMessage(`"not numeric"`)}}},
				DerivedColumns: []recipe.DerivedColumn{{
					ConstructionID: "calc_result", Name: "result", Label: "Result", Operation: recipe.DerivedAdd,
					Left:  recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: test.input},
					Right: integerDerivedOperand(1), MissingInputPolicy: recipe.MissingInputError,
				}},
			}
			_, err := compileDerivedTestBundle(t, output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("compile error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestCompileResolvedRecipePlanCarriesNormalizedUnitsThroughChainedDerivedColumns(t *testing.T) {
	target := unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Aggregates: []recipe.Aggregate{{
			Name: "height", Operation: recipe.AggregateMax,
			Expr: &recipe.Expression{Select: "root.valueQuantity.value"},
			UnitNormalization: &recipe.UnitNormalizationPolicy{
				SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code", Target: target,
				Rules: []unit.UnitRuleReference{{ID: "ucum:m-to-cm", Version: "1"}, {ID: "identity-v1", Version: "1"}},
			},
		}},
		DerivedColumns: []recipe.DerivedColumn{
			{
				ConstructionID: "calc_scaled", Name: "scaled_height", Label: "Scaled height", Operation: recipe.DerivedMultiply,
				Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "height"}, Right: integerDerivedOperand(2),
				MissingInputPolicy: recipe.MissingInputError,
			},
			{
				ConstructionID: "calc_total", Name: "total_height", Label: "Total height", Operation: recipe.DerivedAdd,
				Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "scaled_height"}, Right: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "height"},
				MissingInputPolicy: recipe.MissingInputError,
			},
		},
	}

	compiled := compileDerivedTestOutput(t, output)
	for _, name := range []string{"height", "scaled_height", "total_height"} {
		column, found := outputSchemaColumn(compiled.OutputSchema, name)
		if !found {
			t.Fatalf("output schema is missing %q: %#v", name, compiled.OutputSchema)
		}
		if column.NormalizedUnit == nil || !column.NormalizedUnit.Equal(target) {
			t.Fatalf("normalized unit for %q = %#v, want %#v", name, column.NormalizedUnit, target)
		}
	}
}

func TestCompileResolvedRecipePlanRejectsMismatchedAggregateUnits(t *testing.T) {
	policy := func(target unit.UnitIdentity, rule string) *recipe.UnitNormalizationPolicy {
		rules := []unit.UnitRuleReference{{ID: "identity-v1", Version: "1"}}
		if rule != "" {
			rules = append(rules, unit.UnitRuleReference{ID: rule, Version: "1"})
		}
		return &recipe.UnitNormalizationPolicy{
			SystemPath: "valueQuantity.system", CodePath: "valueQuantity.code", Target: target,
			Rules: rules,
		}
	}
	output := recipe.Output{
		Name: "observations", RootResourceType: "Observation", RowGrain: "observation",
		Aggregates: []recipe.Aggregate{
			{Name: "height", Operation: recipe.AggregateMax, Expr: &recipe.Expression{Select: "root.valueQuantity.value"}, UnitNormalization: policy(unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}, "ucum:m-to-cm")},
			{Name: "mass", Operation: recipe.AggregateMax, Expr: &recipe.Expression{Select: "root.valueQuantity.value"}, UnitNormalization: policy(unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "kg"}, "")},
		},
		DerivedColumns: []recipe.DerivedColumn{{
			ConstructionID: "calc_sum", Name: "sum", Label: "Sum", Operation: recipe.DerivedAdd,
			Left: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "height"}, Right: recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "mass"},
			MissingInputPolicy: recipe.MissingInputError,
		}},
	}
	if _, err := compileDerivedTestBundle(t, output); err == nil || !strings.Contains(err.Error(), "INCOMPATIBLE_UNITS") {
		t.Fatalf("compile error = %v, want unit identity incompatibility", err)
	}
}

func TestCloneCompiledOutputSchemaCopiesNormalizedUnitIdentity(t *testing.T) {
	identity := unit.UnitIdentity{System: "http://unitsofmeasure.org", Code: "cm"}
	source := []CompiledOutputColumn{{Name: "height", NormalizedUnit: &identity}}
	cloned := CloneCompiledOutputSchema(source)
	if cloned[0].NormalizedUnit == source[0].NormalizedUnit {
		t.Fatal("cloned schema shares its normalized unit pointer")
	}
	source[0].NormalizedUnit.Code = "m"
	if cloned[0].NormalizedUnit.Code != "cm" {
		t.Fatalf("cloned unit identity changed with source: %#v", cloned[0].NormalizedUnit)
	}
}

func derivedTestOutput() recipe.Output {
	return recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{
			{Name: "whole", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "fraction", Expr: recipe.Expression{Call: "cast", Args: []recipe.Expression{
				{Select: "id"}, {Literal: json.RawMessage(`"decimal"`)},
			}}},
		},
	}
}

func outputWithDerivedColumns() recipe.Output {
	output := derivedTestOutput()
	output.DerivedColumns = []recipe.DerivedColumn{
		{
			ConstructionID: "calc_scale", Name: "scale", Label: "Scale", Operation: recipe.DerivedMultiply,
			Left:  recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "decimal_sum"},
			Right: integerDerivedOperand(2), MissingInputPolicy: recipe.MissingInputError,
		},
		{
			ConstructionID: "calc_integer_sum", Name: "integer_sum", Label: "Integer sum", Operation: recipe.DerivedAdd,
			Left:  recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "whole"},
			Right: integerDerivedOperand(1), MissingInputPolicy: recipe.MissingInputPropagateNull,
		},
		{
			ConstructionID: "calc_decimal_sum", Name: "decimal_sum", Label: "Decimal sum", Operation: recipe.DerivedAdd,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "whole"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "fraction"},
			MissingInputPolicy: recipe.MissingInputError,
		},
		{
			ConstructionID: "calc_ratio_null", Name: "ratio_null", Label: "Ratio null", Operation: recipe.DerivedDivide,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "whole"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "fraction"},
			MissingInputPolicy: recipe.MissingInputError, DivisionByZeroPolicy: recipe.DivisionByZeroNull,
		},
		{
			ConstructionID: "calc_ratio_error", Name: "ratio_error", Label: "Ratio error", Operation: recipe.DerivedDivide,
			Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "whole"},
			Right:              recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "fraction"},
			MissingInputPolicy: recipe.MissingInputError, DivisionByZeroPolicy: recipe.DivisionByZeroError,
		},
		{
			ConstructionID: "calc_difference", Name: "difference", Label: "Difference", Operation: recipe.DerivedSubtract,
			Left:  recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "whole"},
			Right: integerDerivedOperand(1), MissingInputPolicy: recipe.MissingInputError,
		},
	}
	return output
}

func integerDerivedOperand(value int64) recipe.DerivedOperand {
	return recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: &value}}
}

func compileDerivedTestOutput(t *testing.T, output recipe.Output) CompiledRecipeOutput {
	t.Helper()
	compiled, err := compileDerivedTestBundle(t, output)
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func compileDerivedTestBundle(t *testing.T, output recipe.Output) (CompiledRecipeOutput, error) {
	t.Helper()
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "derived-test", TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		return CompiledRecipeOutput{}, err
	}
	return compiled.Outputs[0], nil
}

func returnProjectionForTest(t *testing.T, plan ir.PhysicalPlan, name string) ir.PhysicalProjection {
	t.Helper()
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == name {
				return projection
			}
		}
	}
	t.Fatalf("physical RETURN has no %q projection", name)
	return ir.PhysicalProjection{}
}

func countExpressionLets(plan ir.PhysicalPlan) int {
	count := 0
	for _, operation := range plan.Operations {
		if operation.Kind == ir.PhysicalExpressionLetOp {
			count++
		}
	}
	return count
}

func outputSchemaColumn(columns []CompiledOutputColumn, name string) (CompiledOutputColumn, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return CompiledOutputColumn{}, false
}

func derivedBindValueContains(bindVars map[string]any, want string) bool {
	for _, value := range bindVars {
		if value == want {
			return true
		}
	}
	return false
}
