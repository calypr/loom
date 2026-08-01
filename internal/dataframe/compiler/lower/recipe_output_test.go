package lower

import (
	"strconv"
	"strings"
	"testing"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
)

func TestCompileResolvedRecipePlanProducesCanonicalPhysicalPlans(t *testing.T) {
	bundle := compilerFixtureBundle(t)
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != len(plan.Outputs) {
		t.Fatalf("compiled output count = %d, want %d", len(compiled.Outputs), len(plan.Outputs))
	}
	for _, output := range compiled.Outputs {
		if len(output.Plan.Operations) == 0 {
			t.Fatalf("output %q has no canonical operations", output.Name)
		}
		if err := output.Plan.Validate(); err != nil {
			t.Fatalf("output %q canonical plan invalid: %v", output.Name, err)
		}
		if output.Plan.Operations[0].Kind != ir.PhysicalRootScanOp {
			t.Fatalf("output %q first operation = %s, want ROOT_SCAN", output.Name, output.Plan.Operations[0].Kind)
		}
	}
}

func TestCompileResolvedRecipePlanLowersDocumentRefEnvelope(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "document-ref",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
			Fields: []recipe.Field{{Name: "resource", Expr: recipe.Expression{Document: &recipe.DocumentRef{Context: "root"}}}},
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d", len(compiled.Outputs))
	}
	var found *ir.PhysicalExpression
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "resource" {
				found = projection.Expression
			}
		}
	}
	if found == nil || found.Kind != ir.PhysicalObjectExpression || found.Object == nil {
		t.Fatalf("resource projection = %#v", found)
	}
	if err := compiled.Outputs[0].Plan.Validate(); err != nil {
		t.Fatalf("document plan validation: %v", err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "payload") || !strings.Contains(rendered.Query, "resourceType") || !strings.Contains(rendered.Query, "_key") {
		t.Fatalf("rendered document envelope missing fields: %s", rendered.Query)
	}
}

func TestCompiledRecipeOutputSchemaMatchesFinalReturnProjectionOrder(t *testing.T) {
	bundle := compilerFixtureBundle(t)
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, output := range compiled.Outputs {
		var projections []ir.PhysicalProjection
		for _, operation := range output.Plan.Operations {
			if operation.Kind == ir.PhysicalReturnOp && operation.Return != nil {
				projections = operation.Return.Projections
				break
			}
		}
		if len(projections) == 0 {
			t.Fatalf("output %q has no return projections", output.Name)
		}
		if len(output.OutputSchema) != len(projections) {
			t.Fatalf("output %q schema length=%d projections=%d", output.Name, len(output.OutputSchema), len(projections))
		}
		for index, projection := range projections {
			column := output.OutputSchema[index]
			if column.Name != projection.Name {
				t.Fatalf("output %q schema[%d]=%q projection=%q", output.Name, index, column.Name, projection.Name)
			}
			wantInternal := projection.Hidden || projection.Name == "_key" || strings.HasPrefix(projection.Name, "__loom_")
			if column.Internal != wantInternal {
				t.Fatalf("output %q column %q internal=%v want %v", output.Name, column.Name, column.Internal, wantInternal)
			}
		}
	}
}

func TestCompileResolvedRecipePlanUsesCanonicalUnnest(t *testing.T) {
	bundle := compilerFixtureBundle(t)
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	group := plan.Outputs[len(plan.Outputs)-1]
	plan.Outputs = []semantic.OutputPlan{group}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, output := range compiled.Outputs {
		if output.Name != "GroupMember" {
			continue
		}
		for _, operation := range output.Plan.Operations {
			if operation.Kind != ir.PhysicalUnnestOp || operation.Unnest == nil {
				continue
			}
			found = true
			if operation.Unnest.InputVariable != "root" || operation.Unnest.OutputVariable != "member" {
				t.Fatalf("unexpected unnest bindings: %#v", operation.Unnest)
			}
		}
	}
	if !found {
		t.Fatal("GroupMember output did not lower to canonical UNNEST")
	}
}

func compilerFixtureBundle(t *testing.T) recipe.Bundle {
	t.Helper()
	bundle, err := recipe.Parse([]byte(`{
		"recipeSchemaVersion":1,
		"name":"compiler-fixture",
		"translationVersion":"test",
		"outputs":[
			{"name":"Patient","rootResourceType":"Patient","rowGrain":"patient","fields":[{"name":"id","expr":{"select":"root.id"}}]},
			{"name":"GroupMember","rootResourceType":"Group","rowGrain":"expanded","expand":{"from":{"select":"root.member[]"},"as":"member"},"identity":{"name":"id","expr":{"select":"root.id"}},"fields":[{"name":"group_id","expr":{"select":"root.id"}},{"name":"member_id","expr":{"select":"root.id"}}]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	return bundle
}

func TestCompileResolvedRecipePlanLowersBoundedDynamicMap(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "dynamic", Source: recipe.Expression{Select: "root.identifier[].value"}, Columns: []string{"x"}}}}}}
	var err error
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 || len(compiled.Outputs[0].DynamicColumns) != 1 {
		t.Fatalf("dynamic metadata = %#v", compiled.Outputs)
	}
	found := false
	foundRuntimeKeys := false
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name == "__loom_dynamic_runtime_keys" && projection.Hidden && projection.Expression != nil && projection.Expression.Kind == ir.PhysicalObjectExpression {
				foundRuntimeKeys = true
			}
			if projection.Name == "dynamic_x" && projection.Expression != nil && projection.Expression.Kind == ir.PhysicalObjectLookupExpression {
				found = true
			}
		}
	}
	if !found || !foundRuntimeKeys {
		t.Fatal("dynamic projection was not lowered to canonical LOOKUP")
	}
}

func TestCompileResolvedRecipePlanLowersDynamicItemLookup(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic-item", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "extension", Source: recipe.Expression{Select: "root.extension[]"}, Key: &recipe.Expression{Select: "item.url"}, Value: &recipe.Expression{Select: "item.url"}, Columns: []string{"http://example.org/code"}}}}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 || len(compiled.Outputs[0].DynamicColumns) != 1 {
		t.Fatalf("dynamic metadata = %#v", compiled.Outputs)
	}
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		for _, projection := range operation.Return.Projections {
			if projection.Name != "extension_http___example_org_code" || projection.Expression == nil || projection.Expression.ObjectLookup == nil {
				continue
			}
			for _, candidate := range compiled.Outputs[0].Plan.Operations {
				if candidate.Kind == ir.PhysicalExpressionLetOp && candidate.ExpressionLet != nil && candidate.ExpressionLet.Expression.KeyedMap != nil {
					if candidate.ExpressionLet.Expression.KeyedMap.ItemKey.Value == nil || candidate.ExpressionLet.Expression.KeyedMap.ItemKey.Value.Path[0] != "url" {
						t.Fatalf("item key was not lowered to dynamic item path: %#v", candidate.ExpressionLet.Expression.KeyedMap.ItemKey)
					}
					return
				}
			}
		}
	}
	t.Fatal("dynamic item lookup projection not found")
}

func TestCompileResolvedRecipePlanRendersDynamicLookupThroughCanonicalRenderer(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "Patient", RootResourceType: "Patient", RowGrain: "patient", Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}}, DynamicColumns: []recipe.DynamicColumn{{Name: "dynamic", Source: recipe.Expression{Select: "root.identifier[].value"}, Columns: []string{"x"}}}}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "__loom_family") || !strings.Contains(rendered.Query, "COLLECT __loom_keyed_group_key") {
		t.Fatalf("canonical renderer omitted lookup shape: %s", rendered.Query)
	}
}

func TestWideDynamicFamilyUsesOneSourceComputation(t *testing.T) {
	columns := make([]string, 150)
	for index := range columns {
		columns[index] = "identifier-" + strconv.Itoa(index)
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "wide-dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		Fields:         []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "root.id"}}},
		DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "root.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Value: &recipe.Expression{Select: "item.value"}, Columns: columns}},
	}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(rendered.Query, "root.payload.identifier"); got != 2 {
		t.Fatalf("wide family source rendered %d times, want one guarded shared computation\n%s", got, rendered.Query)
	}
	if got := strings.Count(rendered.Query, "__loom_family_identifiers["); got != len(columns) {
		t.Fatalf("wide family lookups = %d, want %d", got, len(columns))
	}
	t.Logf("wide family columns=%d aql_bytes=%d bind_vars=%d operations=%d", len(columns), len(rendered.Query), len(rendered.BindVars), len(compiled.Outputs[0].Plan.Operations))
}

func TestCompileResolvedRecipePlanLowersTraversalDynamicMap(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "nested-dynamic", TranslationVersion: "test", Outputs: []recipe.Output{{Name: "ResearchSubject", RootResourceType: "ResearchSubject", RowGrain: "study_enrollment", Traversals: []recipe.Traversal{{Name: "subject_Patient", ToResourceType: "Patient", Alias: "patient", DynamicColumns: []recipe.DynamicColumn{{Name: "identifiers", Source: recipe.Expression{Select: "patient.identifier[]"}, Key: &recipe.Expression{Select: "item.value"}, Columns: []string{"identifier"}}}}}}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs[0].DynamicColumns) != 1 || compiled.Outputs[0].DynamicColumns[0].Name != "patient__identifiers_identifier" {
		t.Fatalf("unexpected traversal dynamic metadata: %#v", compiled.Outputs[0].DynamicColumns)
	}
	if value, ok := compiled.Outputs[0].Plan.BindVars["recipe_dynamic_patient__identifiers_key_0"].(string); !ok || value != "identifier" {
		t.Fatalf("missing traversal dynamic key bind vars: %#v", compiled.Outputs[0].Plan.BindVars)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	foundProjectionName := false
	for _, value := range rendered.BindVars {
		if value == "patient__identifiers_identifier" {
			foundProjectionName = true
			break
		}
	}
	if !foundProjectionName || !strings.Contains(rendered.Query, "FOR __item IN child_set_1") {
		t.Fatalf("rendered traversal dynamic projection missing: query=%s binds=%#v", rendered.Query, rendered.BindVars)
	}
	if strings.Contains(rendered.Query, "FOR dynamic_item IN FLATTEN([(") {
		t.Fatalf("traversal dynamic source retained an extra array wrapper: %s", rendered.Query)
	}
	if !strings.Contains(rendered.Query, "FOR dynamic_item IN FLATTEN((") {
		t.Fatalf("traversal dynamic source was not flattened directly: %s", rendered.Query)
	}
}

func TestCompileResolvedRecipePlanCorrelatesRepeatedPivotItems(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "component-pivot", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
		Pivots: []recipe.Pivot{{Name: "components", ColumnExpr: recipe.Expression{Select: "root.component[].code.coding[].display"}, ValueExpr: recipe.Expression{Select: "root.component[].valueString"}, ItemSource: recipe.Expression{Select: "root.component[]"}, ItemResourceType: "ObservationComponent", Columns: []string{"hemoglobin"}}},
	}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(rendered.Query, "pivot_item_value") < 1 || !strings.Contains(rendered.Query, "__root.code") {
		t.Fatalf("component pivot was not lowered as one correlated item scope: %s", rendered.Query)
	}
}

func TestCompiledRecipeOutputSchemaFlattensMixedObservationPivotToString(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "mixed-observation-pivot", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "Observation", RootResourceType: "Observation", RowGrain: "resource",
		Pivots: []recipe.Pivot{{
			Name:           "observation_values",
			ColumnExpr:     recipe.Expression{Select: "root.code.text"},
			ValueExpr:      recipe.Expression{Select: "root.valueQuantity.value"},
			ValueFallbacks: []recipe.Expression{{Select: "root.valueString"}, {Select: "root.valueBoolean"}},
			Columns:        []string{"Biospecimen"},
		}},
	}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.Name != "observation_values__Biospecimen" {
			continue
		}
		found = true
		if column.Kind != string(expression.KindString) || column.Cardinality != string(expression.RequiredOne) || !column.Nullable {
			t.Fatalf("mixed pivot column = %#v, want nullable scalar string", column)
		}
	}
	if !found {
		t.Fatalf("compiled schema missing flattened pivot column: %#v", compiled.Outputs[0].OutputSchema)
	}
	rendered, err := aql.RenderPhysicalPlan(compiled.Outputs[0].Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered.Query, "TO_STRING(FIRST(__pivot_flat_values))") {
		t.Fatalf("mixed pivot values were not normalized to strings: %s", rendered.Query)
	}
}

func TestCompiledRecipeOutputSchemaHonorsFirstProjectionCardinality(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "first-projection", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "DocumentReference", RootResourceType: "DocumentReference", RowGrain: "resource",
		Fields: []recipe.Field{{
			Name:      "author_reference",
			Expr:      recipe.Expression{Select: "root.author[].reference"},
			ValueMode: recipe.ValueModeFirst,
		}},
	}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range compiled.Outputs[0].OutputSchema {
		if column.Name != "author_reference" {
			continue
		}
		if column.Cardinality != string(expression.OptionalOne) || !column.Nullable {
			t.Fatalf("FIRST projection column = %#v, want optional scalar", column)
		}
		return
	}
	t.Fatalf("compiled schema missing author_reference: %#v", compiled.Outputs[0].OutputSchema)
}

func TestCompileResolvedRecipePlanCarriesRichShapingIntoCanonicalIR(t *testing.T) {
	bundle := recipe.Bundle{
		RecipeSchemaVersion: 1,
		Name:                "rich",
		TranslationVersion:  "test",
		Outputs: []recipe.Output{{
			Name:             "conditions",
			RootResourceType: "Condition",
			RowGrain:         "diagnosis",
			Fields:           []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
			Pivots: []recipe.Pivot{{
				Name:       "diagnosis",
				ColumnExpr: recipe.Expression{Select: "code.coding[].display"},
				ValueExpr:  recipe.Expression{Select: "code.text"},
				Columns:    []string{"melanoma", "glioma"},
			}},
			Aggregates: []recipe.Aggregate{{Name: "condition_count", Operation: recipe.AggregateCount}},
			Slices: []recipe.RepresentativeSlice{{
				Name: "representatives", Limit: 2,
				Fields: []recipe.Field{{Name: "status", Expr: recipe.Expression{Select: "verificationStatus"}}},
			}},
		}},
	}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(compiled.Outputs) != 1 {
		t.Fatalf("compiled outputs = %d, want 1", len(compiled.Outputs))
	}
	var kinds map[string]ir.PhysicalExpressionKind
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind != ir.PhysicalReturnOp || operation.Return == nil {
			continue
		}
		kinds = make(map[string]ir.PhysicalExpressionKind, len(operation.Return.Projections))
		for _, projection := range operation.Return.Projections {
			if projection.Expression != nil {
				kinds[projection.Name] = projection.Expression.Kind
			}
		}
	}
	for name, want := range map[string]ir.PhysicalExpressionKind{
		"diagnosis__glioma":   ir.PhysicalObjectLookupExpression,
		"diagnosis__melanoma": ir.PhysicalObjectLookupExpression,
		"condition_count":     ir.PhysicalAggregateExpression,
		"representatives":     ir.PhysicalSliceExpression,
	} {
		if got := kinds[name]; got != want {
			t.Fatalf("projection %q kind = %q, want %q (all=%#v)", name, got, want, kinds)
		}
	}
}

func TestCompileResolvedRecipePlanPreservesRestrictedEmptyAuthScope(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "scope", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
	}}}
	for _, test := range []struct {
		name       string
		mode       authscope.ReadScopeMode
		wantBypass bool
	}{
		{name: "restricted-empty", mode: authscope.ReadScopeRestricted, wantBypass: false},
		{name: "legacy-unrestricted", mode: authscope.ReadScopeUnrestricted, wantBypass: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			bindings := recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation", AuthScopeMode: test.mode}
			plan, err := semantic.BuildRecipePlan(bundle, bindings)
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
			if err != nil {
				t.Fatal(err)
			}
			compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
			if err != nil {
				t.Fatal(err)
			}
			got, ok := compiled.Outputs[0].Plan.BindVars["auth_resource_paths_unrestricted"].(bool)
			if !ok || got != test.wantBypass {
				t.Fatalf("auth bypass bind = %#v, want %t", compiled.Outputs[0].Plan.BindVars["auth_resource_paths_unrestricted"], test.wantBypass)
			}
		})
	}
}

func TestCompileResolvedRecipePlanLowersRequiredTraversalFilter(t *testing.T) {
	bundle := recipe.Bundle{RecipeSchemaVersion: 1, Name: "required", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "patients", RootResourceType: "Patient", RowGrain: "patient",
		Fields: []recipe.Field{{Name: "id", Expr: recipe.Expression{Select: "id"}}},
		Traversals: []recipe.Traversal{{
			Name: "subject_Patient", Alias: "condition", ToResourceType: "Condition", MatchMode: recipe.MatchRequired,
			Filters: []recipe.Filter{{Select: "id", Operator: recipe.FilterExists}},
		}},
	}}}
	plan, err := semantic.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "project", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, operation := range compiled.Outputs[0].Plan.Operations {
		if operation.Kind == ir.PhysicalFilterOp && operation.Filter != nil && operation.Source.SemanticNode == "condition" {
			found = true
		}
	}
	if !found {
		t.Fatal("required traversal filter did not reach physical plan")
	}
}
