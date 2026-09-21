package lower

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCompileGroupedTablePivotUsesFrozenTypedCategoriesAndOrderedOutputs(t *testing.T) {
	output := tableReshapeTestOutput(&recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "shape_pivot", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories: []recipe.GroupedPivotCategory{
				{Key: tableScalarInteger(0), Output: "birth_zero", Label: "Zero"},
				{Key: tableScalarInteger(1), Output: "birth_one", Label: "One"},
			},
			DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryExcludeWithEvidence,
		},
	})
	compiled := compileDerivedTestOutput(t, output)

	wantPublic := []string{"group", "birth_zero", "birth_one"}
	if got := compiledPublicSchemaNames(compiled.OutputSchema); !reflect.DeepEqual(got, wantPublic) {
		t.Fatalf("public columns = %#v, want %#v", got, wantPublic)
	}
	wantSchema := []string{"group", "birth_zero", "birth_one", "__loom_reshape_unlisted_count", "__loom_row_id"}
	if got := compiledSchemaNames(compiled.OutputSchema); !reflect.DeepEqual(got, wantSchema) {
		t.Fatalf("output schema order = %#v, want %#v", got, wantSchema)
	}
	if column, ok := outputSchemaColumn(compiled.OutputSchema, "birth_zero"); !ok || column.Kind != string(expression.KindInteger) || !column.Nullable || column.SemanticPath != "table_reshape:shape_pivot:category:birth_zero" {
		t.Fatalf("pivot category schema = %#v, found=%t", column, ok)
	}

	var pivot *ir.PhysicalGroupedPivot
	for index := range compiled.Plan.Operations {
		operation := &compiled.Plan.Operations[index]
		if operation.Kind == ir.PhysicalGroupedPivotOp {
			pivot = operation.GroupedPivot
			if index+1 >= len(compiled.Plan.Operations) || compiled.Plan.Operations[index+1].Kind != ir.PhysicalReturnOp {
				t.Fatalf("grouped pivot is not terminal before RETURN: %#v", compiled.Plan.Operations)
			}
		}
	}
	if pivot == nil || pivot.ConstructionID != "shape_pivot" || pivot.GroupKeys[0].Column != "group" || pivot.CategoryColumn != "category" || pivot.ValueColumn != "value" {
		t.Fatalf("grouped pivot lost direct construction/input identity: %#v", pivot)
	}
	if got := compiledProjectionNames(compiled.Plan); !reflect.DeepEqual(got, wantSchema) {
		t.Fatalf("physical output projection order = %#v, want %#v", got, wantSchema)
	}

	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"COLLECT", "TYPENAME(", "SUM(", "LENGTH(", "? null :", "TO_STRING([\"GROUPED_PIVOT\""} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered pivot query missing %q: %s", expected, rendered.Query)
		}
	}
	if !bindContainsExactValue(rendered.BindVars, int64(0)) {
		t.Fatalf("integer zero category key was not preserved as an exact bind: %#v", rendered.BindVars)
	}
	if !strings.Contains(rendered.Query, `TO_STRING(["GROUPED_PIVOT", @`+pivot.ConstructionIDBindKey) || !strings.Contains(rendered.Query, fmt.Sprintf(`["STRING", %s]`, pivot.GroupKeys[0].Variable)) {
		t.Fatalf("pivot identity is not the construction ID plus a typed group-key tuple: %s", rendered.Query)
	}
	assertUnlistedCategoryScope(t, rendered.Query, pivot)
}

func TestCompileGroupedTablePivotRendersDuplicateAndMissingErrors(t *testing.T) {
	output := tableReshapeTestOutput(&recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "shape_pivot_error", GroupKeys: []string{"group"}, CategoryColumn: "category", ValueColumn: "value",
			Categories:      []recipe.GroupedPivotCategory{{Key: tableScalarInteger(0), Output: "zero", Label: "Zero"}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellError,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	compiled := compileDerivedTestOutput(t, output)
	rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"TABLE_PIVOT_CELL_CARDINALITY", "TABLE_PIVOT_CELL_MISSING", "TABLE_PIVOT_UNLISTED_CATEGORY", "LENGTH(", "FIRST("} {
		if !strings.Contains(rendered.Query, expected) {
			t.Fatalf("rendered error-policy query missing %q: %s", expected, rendered.Query)
		}
	}
}

func TestCompileUnpivotPreservesColumnOrderNullPolicyAndIdentity(t *testing.T) {
	for _, test := range []struct {
		name         string
		policy       recipe.UnpivotNullRowPolicy
		wantNullable bool
		wantFilter   bool
	}{
		{name: "preserve null", policy: recipe.UnpivotNullPreserve, wantNullable: true},
		{name: "drop null", policy: recipe.UnpivotNullDrop, wantFilter: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			output := tableReshapeTestOutput(&recipe.TableReshape{
				Kind: recipe.TableReshapeUnpivot,
				Unpivot: &recipe.Unpivot{
					ConstructionID: "shape_unpivot", Inputs: []recipe.UnpivotInput{
						{Column: "value2", Key: tableScalarString("secondary")},
						{Column: "value", Key: tableScalarString("primary")},
					},
					KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: test.policy,
				},
			})
			compiled := compileDerivedTestOutput(t, output)
			wantColumns := []string{"group", "category", "keep", "measure", "amount"}
			if got := compiledPublicSchemaNames(compiled.OutputSchema); !reflect.DeepEqual(got, wantColumns) {
				t.Fatalf("public columns = %#v, want %#v", got, wantColumns)
			}
			wantSchema := []string{"_key", "group", "category", "keep", "measure", "amount", "__loom_row_id"}
			if got := compiledSchemaNames(compiled.OutputSchema); !reflect.DeepEqual(got, wantSchema) {
				t.Fatalf("output schema order = %#v", got)
			}
			if column, ok := outputSchemaColumn(compiled.OutputSchema, "amount"); !ok || column.Nullable != test.wantNullable {
				t.Fatalf("unpivot value schema = %#v, found=%t", column, ok)
			}
			unpivot := compiledUnpivot(t, compiled.Plan)
			if unpivot.ConstructionID != "shape_unpivot" || len(unpivot.Inputs) != 2 || unpivot.Inputs[0].Column != "value2" || unpivot.Inputs[1].Column != "value" {
				t.Fatalf("unpivot lost authored construction/input order: %#v", unpivot)
			}
			if len(unpivot.IdentityParts) < 2 {
				t.Fatalf("unpivot has no stable input identity parts: %#v", unpivot.IdentityParts)
			}
			rendered, err := aql.RenderPhysicalPlan(compiled.Plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, expected := range []string{"FOR " + unpivot.SlotVariable + " IN [", "TO_STRING([", "@" + unpivot.ConstructionIDBindKey, `["STRING", ` + unpivot.SlotVariable + ".key]"} {
				if !strings.Contains(rendered.Query, expected) {
					t.Fatalf("rendered unpivot query missing %q: %s", expected, rendered.Query)
				}
			}
			if test.wantFilter != strings.Contains(rendered.Query, "FILTER "+unpivot.SlotVariable+".value != null") {
				t.Fatalf("null-row filter presence=%t, want %t: %s", strings.Contains(rendered.Query, "FILTER "+unpivot.SlotVariable+".value != null"), test.wantFilter, rendered.Query)
			}
		})
	}
}

func TestCompileTableReshapeRejectsInvalidCombinationsAndTypes(t *testing.T) {
	pivot := func(category, value string, duplicate recipe.PivotDuplicatePolicy, key recipe.TableScalar) *recipe.TableReshape {
		return &recipe.TableReshape{
			Kind: recipe.TableReshapeGroupedPivot,
			GroupedPivot: &recipe.GroupedPivot{
				ConstructionID: "shape_invalid", GroupKeys: []string{"group"}, CategoryColumn: category, ValueColumn: value,
				Categories:      []recipe.GroupedPivotCategory{{Key: key, Output: "out", Label: "Output"}},
				DuplicatePolicy: duplicate, MissingCellPolicy: recipe.PivotMissingCellNull,
				UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
			},
		}
	}
	unpivot := &recipe.TableReshape{
		Kind: recipe.TableReshapeUnpivot,
		Unpivot: &recipe.Unpivot{
			ConstructionID: "shape_invalid", Inputs: []recipe.UnpivotInput{{Column: "value", Key: tableScalarString("value")}},
			KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve,
		},
	}
	for _, test := range []struct {
		name   string
		output recipe.Output
		want   string
	}{
		{name: "category type mismatch", output: tableReshapeTestOutput(pivot("category", "value", recipe.PivotDuplicateError, tableScalarString("zero"))), want: "category key 0 type"},
		{name: "nonnumeric reducer", output: tableReshapeTestOutput(pivot("category", "keep", recipe.PivotDuplicateSum, tableScalarInteger(0))), want: "requires a numeric value column"},
		{name: "incompatible unpivot inputs", output: tableReshapeTestOutput(&recipe.TableReshape{
			Kind: recipe.TableReshapeUnpivot,
			Unpivot: &recipe.Unpivot{ConstructionID: "shape_invalid", Inputs: []recipe.UnpivotInput{
				{Column: "value", Key: tableScalarString("number")}, {Column: "group", Key: tableScalarString("text")},
			}, KeyOutput: "measure", KeyLabel: "Measure", ValueOutput: "amount", ValueLabel: "Amount", NullRowPolicy: recipe.UnpivotNullPreserve},
		}), want: "incompatible with previous inputs"},
		{name: "derived with unpivot", output: func() recipe.Output {
			result := tableReshapeTestOutput(unpivot)
			result.DerivedColumns = []recipe.DerivedColumn{{
				ConstructionID: "calc_after_unpivot", Name: "derived", Label: "Derived", Operation: recipe.DerivedAdd,
				Left:               recipe.DerivedOperand{Kind: recipe.DerivedColumnOperand, Column: "keep"},
				Right:              recipe.DerivedOperand{Kind: recipe.DerivedLiteralOperand, Literal: &recipe.DerivedLiteral{Kind: recipe.NumericInteger, Integer: int64Pointer(1)}},
				MissingInputPolicy: recipe.MissingInputError,
			}}
			return result
		}(), want: "unpivot cannot be combined with derived columns"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := compileDerivedTestBundle(t, test.output)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("compile error = %v, want %q", err, test.want)
			}
		})
	}
}

func tableReshapeTestOutput(reshape *recipe.TableReshape) recipe.Output {
	return recipe.Output{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient", TableReshape: reshape,
		Fields: []recipe.Field{
			{Name: "group", Expr: recipe.Expression{Select: "gender"}},
			{Name: "category", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "value", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "value2", Expr: recipe.Expression{Select: "multipleBirthInteger"}},
			{Name: "keep", Expr: recipe.Expression{Select: "id"}},
		},
	}
}

func tableScalarString(value string) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarString, String: &value}
}

func tableScalarInteger(value int64) recipe.TableScalar {
	return recipe.TableScalar{Kind: recipe.TableScalarInteger, Integer: &value}
}

func int64Pointer(value int64) *int64 { return &value }

func compiledSchemaNames(schema []CompiledOutputColumn) []string {
	names := make([]string, 0, len(schema))
	for _, column := range schema {
		names = append(names, column.Name)
	}
	return names
}

func compiledPublicSchemaNames(schema []CompiledOutputColumn) []string {
	names := make([]string, 0, len(schema))
	for _, column := range schema {
		if !column.Internal {
			names = append(names, column.Name)
		}
	}
	return names
}

func compiledProjectionNames(plan ir.PhysicalPlan) []string {
	for _, operation := range plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		result := make([]string, 0, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			result = append(result, projection.Name)
		}
		return result
	}
	return nil
}

func compiledUnpivot(t *testing.T, plan ir.PhysicalPlan) *ir.PhysicalUnpivot {
	t.Helper()
	for index := range plan.Operations {
		if plan.Operations[index].Kind == ir.PhysicalUnpivotOp {
			return plan.Operations[index].Unpivot
		}
	}
	t.Fatal("physical plan has no UNPIVOT operation")
	return nil
}

func bindContainsExactValue(bindVars map[string]any, want any) bool {
	for _, value := range bindVars {
		if reflect.DeepEqual(value, want) {
			return true
		}
	}
	return false
}

func assertUnlistedCategoryScope(t *testing.T, query string, pivot *ir.PhysicalGroupedPivot) {
	t.Helper()
	var line string
	for _, candidate := range strings.Split(query, "\n") {
		if strings.Contains(candidate, "FILTER NOT") && strings.Contains(candidate, "reshape_unlisted_item") {
			line = candidate
			break
		}
	}
	if line == "" {
		t.Fatalf("rendered query has no unlisted-category filter: %s", query)
	}
	forPosition := strings.Index(line, "FOR ")
	iterator := strings.Fields(line[forPosition+len("FOR "):])[0]
	categoryReferences := regexp.MustCompile(`([A-Za-z_][A-Za-z0-9_]*)\.` + regexp.QuoteMeta(pivot.CategoryColumn))
	for _, reference := range categoryReferences.FindAllStringSubmatch(line, -1) {
		if reference[1] != iterator {
			t.Fatalf("unlisted-category predicate references %q instead of its iterator %q: %s", reference[1], iterator, line)
		}
	}
	for _, category := range pivot.Categories {
		if !strings.Contains(line, "@"+category.KeyBindKey) {
			t.Fatalf("unlisted-category predicate omits frozen bind %q: %s", category.KeyBindKey, line)
		}
	}
	if strings.Contains(line, "reshape_cell_") {
		t.Fatalf("unlisted-category predicate references an out-of-scope category iterator: %s", line)
	}
	if !strings.Contains(line, "TYPENAME(") {
		t.Fatalf("unlisted-category predicate omits typed category equality: %s", line)
	}
}
