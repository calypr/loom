package resolver

import (
	"context"
	"testing"

	"github.com/calypr/loom/generated/graphql/graph/model"
	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	materialization "github.com/calypr/loom/internal/dataframe/publication"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	"github.com/calypr/loom/internal/dataframe/spec"
	dataset "github.com/calypr/loom/internal/dataset"
)

type fakeRecipeControl struct {
	validation   dataframeexecution.Validation
	plan         semantic.ResolvedRecipePlan
	resolveCalls *int
}

func (f fakeRecipeControl) ExplainPhysical(context.Context, string, recipe.RuntimeBindings, bool) (dataframeexecution.PhysicalExplanation, error) {
	return dataframeexecution.PhysicalExplanation{}, nil
}

func (f fakeRecipeControl) Run(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	rows := []map[string]any{{"id": "p1"}, {"id": "p2"}}
	return dataframeexecution.Preview{Plan: f.plan, Outputs: []dataframeexecution.OutputRows{{Name: "rows", Columns: []string{"id"}, Rows: rows}}}, nil
}

func (f fakeRecipeControl) Validate(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Validation, error) {
	return f.validation, nil
}
func (f fakeRecipeControl) Explain(context.Context, string, recipe.RuntimeBindings) (semantic.RecipePlanExplanation, error) {
	return f.validation.Plan.Explain(), nil
}
func (f fakeRecipeControl) Resolve(context.Context, string, recipe.RuntimeBindings) (semantic.ResolvedRecipePlan, error) {
	if f.resolveCalls != nil {
		(*f.resolveCalls)++
	}
	return f.plan, nil
}
func (f fakeRecipeControl) Preview(context.Context, string, recipe.RuntimeBindings) (dataframeexecution.Preview, error) {
	rows := []map[string]any{{"id": "p1"}}
	return dataframeexecution.Preview{Plan: f.plan, Outputs: []dataframeexecution.OutputRows{{Name: "rows", Columns: []string{"id"}, Rows: rows}}}, nil
}

func testRecipeValidation() dataframeexecution.Validation {
	plan := semantic.RecipePlan{Version: 1, RecipeDigest: "recipe", TranslationVersion: "legacy", Outputs: []semantic.OutputPlan{{
		Name: "rows", RootResourceType: "Patient", RowGrain: spec.RowGrainResource,
		Root: semantic.SemanticNode{Alias: "root", ResourceType: "Patient", Fields: []semantic.SemanticField{{Name: "id", Expr: semantic.SemanticExpression{SourcePath: "$.outputs[0].fields[0]", Type: semanticTypeString().Type}}}},
	}}}
	return dataframeexecution.Validation{Plan: plan}
}

func semanticTypeString() (value semantic.SemanticExpression) {
	value.Type.Kind = "string"
	value.Type.Cardinality = "required_one"
	return value
}

func TestRecipeControlResolverReturnsLogicalValidation(t *testing.T) {
	validation := testRecipeValidation()
	resolver := NewResolver(ResolverConfig{RecipeControl: fakeRecipeControl{validation: validation, plan: semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}}})
	result, err := resolver.Mutation().ValidateDataframeRecipe(context.Background(), model.ValidateDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RecipeDigest != "recipe" || len(result.Outputs) != 1 || result.Outputs[0].FieldNames[0] != "id" {
		t.Fatalf("unexpected validation result: %#v", result)
	}
}

func TestRecipeControlResolverRunsCompleteRecipe(t *testing.T) {
	validation := testRecipeValidation()
	plan := semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}
	resolver := NewResolver(ResolverConfig{RecipeControl: fakeRecipeControl{validation: validation, plan: plan}})
	result, err := resolver.Mutation().RunDataframeRecipe(context.Background(), model.RunDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != "default" || len(result.Outputs) != 1 || result.Outputs[0].RowCount != 2 {
		t.Fatalf("unexpected full recipe result: %#v", result)
	}
}

func TestDataframeCSVUsesResolvedColumnsAndEscapesValues(t *testing.T) {
	value, err := dataframeCSV([]string{"id", "label", "tags"}, []map[string]any{{
		"id": "p1", "label": "a,b", "tags": []string{"x", "y"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := `id,label,tags
p1,"a,b","[""x"",""y""]"
`
	if value != want {
		t.Fatalf("unexpected CSV output: %q", value)
	}
}

func TestLogicalPreviewRowsKeepsNestedCompilerColumnsAndDropsInternalValues(t *testing.T) {
	columns := []string{"id", "specimen__type", "specimen__observation__code"}
	rows := logicalPreviewRows(columns, []map[string]any{{
		"id": "p1", "specimen__type": "tumor", "specimen__observation__code": "C1",
		"__loom_row_id": "internal", "unplanned": "must not leak",
	}})
	if len(rows) != 1 || rows[0]["specimen__observation__code"] != "C1" {
		t.Fatalf("nested compiler column was lost: %#v", rows)
	}
	if _, ok := rows[0]["__loom_row_id"]; ok {
		t.Fatalf("internal identity leaked: %#v", rows[0])
	}
	if _, ok := rows[0]["unplanned"]; ok {
		t.Fatalf("unplanned row key leaked: %#v", rows[0])
	}
	csvValue, err := dataframeCSV(columns, rows)
	if err != nil {
		t.Fatal(err)
	}
	if csvValue != "id,specimen__type,specimen__observation__code\np1,tumor,C1\n" {
		t.Fatalf("nested compiler column missing from CSV: %q", csvValue)
	}
}

func TestRecipeControlPreflightDoesNotExposePhysicalDetails(t *testing.T) {
	validation := testRecipeValidation()
	plan := semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}
	resolver := NewResolver(ResolverConfig{RecipeControl: fakeRecipeControl{validation: validation, plan: plan}})
	result, err := resolver.Query().PreflightDataframeRecipe(context.Background(), model.PreflightDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ResolvedSchemaDigest != "schema" || result.SourceGeneration != "g" || result.ScopeDigest != "scope" {
		t.Fatalf("unexpected preflight result: %#v", result)
	}
}

type fakeRecipeExecutionStore struct {
	execution materialization.BundleExecution
}

func (s fakeRecipeExecutionStore) GetExecution(context.Context, string) (materialization.BundleExecution, error) {
	return s.execution, nil
}

func TestRecipeExecutionReaderMapsDurableBundleState(t *testing.T) {
	reader := NewAuthorizedRecipeExecutionReader(fakeRecipeExecutionStore{execution: materialization.BundleExecution{
		ID: "execution-1", BundleIdentity: materialization.BundleIdentity{
			Name: "default", Project: "p", DatasetGeneration: "g", RecipeDigest: "recipe", SchemaDigest: "schema",
		}, State: materialization.BundleReady,
		Outputs: []materialization.BundleOutputRecord{{Name: "Patient", State: materialization.BundleReady, RowCount: 7}},
	}}, nil)
	execution, err := reader(context.Background(), "execution-1")
	if err != nil {
		t.Fatal(err)
	}
	if execution.ID != "execution-1" || execution.State != "READY" || execution.ResolvedSchemaDigest != "schema" || len(execution.Outputs) != 1 || *execution.Outputs[0].RowCount != 7 {
		t.Fatalf("unexpected execution: %#v", execution)
	}
}

func TestRecipeExecutionReaderMapsCanonicalAsyncStates(t *testing.T) {
	for state, want := range map[materialization.BundleState]string{
		materialization.BundleQueued: "QUEUED", materialization.BundleRunning: "RUNNING",
		materialization.BundleValidating: "VALIDATING", materialization.BundlePublished: "PUBLISHED",
		materialization.BundleFailed: "FAILED",
	} {
		got, err := graphQLRecipeExecutionState(state)
		if err != nil || got != want {
			t.Fatalf("state %s = %q, %v; want %q", state, got, err, want)
		}
	}
}

func TestMaterializeRecipeDoesNotPreResolveInGraphQL(t *testing.T) {
	validation := testRecipeValidation()
	plan := semantic.ResolvedRecipePlan{SemanticPlan: validation.Plan, ResolvedSchemaDigest: "schema", SourceGeneration: "g", ScopeDigest: "scope"}
	resolveCalls := 0
	materializeCalls := 0
	resolver := NewResolver(ResolverConfig{
		RecipeControl: fakeRecipeControl{validation: validation, plan: plan, resolveCalls: &resolveCalls},
		RecipeMaterialize: func(context.Context, string, recipe.RuntimeBindings) (RecipeExecution, error) {
			materializeCalls++
			return RecipeExecution{ID: "execution-1", Name: "default", State: "READY"}, nil
		},
	})
	if _, err := resolver.Mutation().MaterializeDataframeRecipeBundle(context.Background(), model.MaterializeDataframeRecipeInput{
		Name: "default", Bindings: &model.DataframeRecipeBindingsInput{Project: "p", DatasetGeneration: stringPtr("g")},
	}); err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 0 || materializeCalls != 1 {
		t.Fatalf("GraphQL pre-resolve counts = resolve:%d materialize:%d", resolveCalls, materializeCalls)
	}
}

func TestStartDataframeMaterializationPassesExactSelector(t *testing.T) {
	var got dataset.DataframeSelector
	resolver := NewResolver(ResolverConfig{ExactMaterializationStarter: func(_ context.Context, selector dataset.DataframeSelector, bindings recipe.RuntimeBindings) (RecipeExecution, error) {
		got = selector
		if bindings.Project != "p" || bindings.DatasetGeneration != "git-sha" {
			t.Fatalf("target = %s/%s", bindings.Project, bindings.DatasetGeneration)
		}
		return RecipeExecution{ID: "execution-1", Name: selector.Recipe, TranslationVersion: selector.TranslationVersion, State: "QUEUED", Outputs: []RecipeExecutionOutput{{Name: selector.Output, Selector: selector, State: "QUEUED"}}}, nil
	}})
	result, err := resolver.Mutation().StartDataframeMaterialization(context.Background(), model.StartDataframeMaterializationInput{Project: "p", Generation: "git-sha", Selector: &model.DataframeSelectorInput{Recipe: "documents", TranslationVersion: "v2", Output: "DocumentReference"}})
	if err != nil {
		t.Fatal(err)
	}
	if !got.Valid() || result.TranslationVersion == nil || *result.TranslationVersion != "v2" || result.Outputs[0].Selector == nil {
		t.Fatalf("result = %#v, selector = %#v", result, got)
	}
}

func stringPtr(value string) *string { return &value }
