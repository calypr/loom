package differential

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/compilerresearch/reference"
	"github.com/calypr/loom/internal/dataframe/compiler"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/research/compilerprobe"
	store "github.com/calypr/loom/internal/store/arango"
)

func TestProjectConstructionChainMatchesReferenceAndArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to a disposable Arango database")
	}
	if os.Getenv("COMPILER_ORACLE_ALLOW_WRITE") != "1" {
		t.Skip("set COMPILER_ORACLE_ALLOW_WRITE=1 to run the write-enabled oracle")
	}
	if !strings.HasPrefix(database, "compiler_oracle_") {
		t.Fatalf("refusing oracle writes to non-dedicated database %q", database)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "compiler_oracle_" + randomID()
	generation := "compiler-oracle-generation"
	documents := []map[string]any{
		{"id": "alpha-a", "status": "final", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 2.5}},
		{"id": "alpha-b", "status": "final", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 4.5}},
		{"id": "beta", "status": "final", "code": map[string]any{"text": "beta"}, "valueQuantity": map[string]any{"value": 3.5}},
		{"id": "small", "status": "small", "code": map[string]any{"text": "alpha"}, "valueQuantity": map[string]any{"value": 1.0}},
	}
	raw := make([]json.RawMessage, 0, len(documents))
	for _, payload := range documents {
		doc := map[string]any{"_key": project + "_" + payload["id"].(string), "id": payload["id"], "resourceType": "Observation", "project": project, "project_id": project, "dataset_generation": generation, "payload": payload}
		encoded, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		raw = append(raw, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", raw, false, "document"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := client.ExecuteAQL(cleanupCtx, "FOR d IN Observation FILTER d.project == @project REMOVE d IN Observation", map[string]any{"project": project}); err != nil {
			t.Errorf("remove owned fixture: %v", err)
		}
	})

	bundle, bindings := chainRecipe(project, generation)
	compiled := compileBundle(t, bundle, bindings)
	translated, err := compilerprobe.Translate(bundle)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := compilerprobe.RestoreTransport(translated)
	if err != nil {
		t.Fatal(err)
	}
	transported := compileBundle(t, restored, bindings)
	if compiled.Query != transported.Query || !reflect.DeepEqual(compiled.BindVars, transported.BindVars) {
		t.Fatal("v2 transport changed the current emitter's query or binds")
	}
	want, err := reference.Evaluate(bundle.Outputs[0], toReferenceDocuments(documents))
	if err != nil {
		t.Fatal(err)
	}
	wantRows := canonicalRows(visibleRows(want))
	for _, candidate := range []struct {
		name  string
		query string
		binds map[string]any
	}{
		{"current", compiled.Query, compiled.BindVars},
		{"transport", transported.Query, transported.BindVars},
	} {
		var actual []map[string]any
		if err := client.QueryRows(ctx, candidate.query, 100, candidate.binds, func(row map[string]any) error {
			actual = append(actual, row)
			return nil
		}); err != nil {
			t.Fatalf("%s query: %v", candidate.name, err)
		}
		actualRows := canonicalRows(visibleArangoRows(actual))
		if !reflect.DeepEqual(wantRows, actualRows) {
			t.Fatalf("%s and reference differ\nreference=%s\narango=%s\nquery=%s", candidate.name, pretty(wantRows), pretty(actualRows), candidate.query)
		}
	}
	t.Logf("current and transport results matched the reference for %d public rows from %d documents", len(wantRows), len(documents))
}

func compileBundle(t *testing.T, bundle recipe.Bundle, bindings recipe.RuntimeBindings) model.CompiledQuery {
	t.Helper()
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, bindings.Project, bindings.DatasetGeneration)
	if err != nil {
		t.Fatal(err)
	}
	lowered, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.CompileRecipeOutputWithPolicy(lowered.Outputs[0], bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func chainRecipe(project, generation string) (recipe.Bundle, recipe.RuntimeBindings) {
	alpha, beta := "alpha", "beta"
	threshold := 10.0
	output := recipe.Output{Name: "summary", RootResourceType: "Observation", RowGrain: "observation", RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}, {Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.code.text"}}, {Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}}},
		Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "category_id", Name: "category"}, {ID: "amount_id", Name: "amount"}}, Steps: []recipe.ConstructionStep{
			{ID: "pivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionPivotOp, Pivot: &recipe.ConstructionPivot{ConstructionID: "pivot", GroupKeyIDs: []string{"status_id"}, CategoryColumnID: "category_id", ValueColumnID: "amount_id", Categories: []recipe.ConstructionPivotCategory{{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}, OutputColumnID: "alpha_id"}, {Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}, OutputColumnID: "beta_id"}}, DuplicatePolicy: recipe.PivotDuplicateSum, MissingCellPolicy: recipe.PivotMissingCellNull, UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}}},
			{ID: "derive", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "pivot"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionDeriveOp, Derive: &recipe.ConstructionDerive{ConstructionID: "total", OutputColumnID: "total_id", Operation: recipe.DerivedAdd, Left: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "alpha_id"}, Right: recipe.ConstructionOperand{Kind: recipe.DerivedColumnOperand, ColumnID: "beta_id"}, MissingInputPolicy: recipe.MissingInputPropagateNull}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"}}},
			{ID: "filter", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "derive"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{ColumnID: "total_id", Operator: recipe.FilterGreaterEq, Values: []recipe.FilterValue{{Kind: recipe.FilterDecimal, Decimal: &threshold}}}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "alpha_id", Name: "alpha"}, {ID: "beta_id", Name: "beta"}, {ID: "total_id", Name: "total"}}},
			{ID: "unpivot", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "filter"}}, Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionUnpivotOp, Unpivot: &recipe.ConstructionUnpivot{ConstructionID: "unpivot", Inputs: []recipe.ConstructionUnpivotInput{{ColumnID: "alpha_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &alpha}}, {ColumnID: "beta_id", Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &beta}}}, KeyOutputColumnID: "measure_id", ValueOutputColumnID: "value_id", NullRowPolicy: recipe.UnpivotNullPreserve}}, Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "total_id", Name: "total"}, {ID: "measure_id", Name: "measure"}, {ID: "value_id", Name: "value"}}},
		}},
	}
	return recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "compiler-oracle", TranslationVersion: "research", Outputs: []recipe.Output{output}}, recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
}

func toReferenceDocuments(rows []map[string]any) []reference.Document {
	out := make([]reference.Document, 0, len(rows))
	for _, row := range rows {
		out = append(out, reference.Document(row))
	}
	return out
}
func visibleRows(rows []reference.Row) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		visible := map[string]any{}
		for key, value := range row {
			if value.Present {
				visible[key] = value.Data
			} else {
				visible[key] = nil
			}
		}
		out = append(out, visible)
	}
	return out
}
func visibleArangoRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		visible := map[string]any{}
		for _, key := range []string{"status", "total", "measure", "value"} {
			visible[key] = row[key]
		}
		out = append(out, visible)
	}
	return out
}
func canonicalRows(rows []map[string]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		encoded, err := json.Marshal(row)
		if err != nil {
			panic(err)
		}
		out = append(out, string(encoded))
	}
	sort.Strings(out)
	return out
}
func pretty(value any) string {
	encoded, _ := json.MarshalIndent(value, "", "  ")
	return string(encoded)
}
func randomID() string {
	return fmt.Sprintf("%08x", rand.New(rand.NewSource(time.Now().UnixNano())).Uint32())
}
