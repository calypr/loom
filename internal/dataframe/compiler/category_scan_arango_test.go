package compiler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/semantic"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestConstructionCategoryScanPreservesMissingAndNullAgainstArango(t *testing.T) {
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Patient"}}}); err != nil {
		t.Fatal(err)
	}

	project, generation := "loom_category_presence_"+uuid.NewString(), "generation-category-presence"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Patient FILTER document.project == @project REMOVE document IN Patient",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove category presence fixtures: %v", err)
		}
	}()

	fixtures := []struct {
		id      string
		active  any
		include bool
	}{
		{id: "missing_category", active: true, include: true},
		{id: "null_category", active: true, include: true},
		{id: "filtered_out", include: false},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		payload := map[string]any{"resourceType": "Patient", "id": fixture.id}
		if fixture.include {
			payload["active"] = fixture.active
		}
		if fixture.id == "null_category" {
			payload["birthDate"] = nil
		}
		if fixture.id == "filtered_out" {
			payload["birthDate"] = "1990-01-01"
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + fixture.id, "id": fixture.id, "project": project,
			"project_id": project, "dataset_generation": generation,
			"resourceType": "Patient", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Patient", documents, false, "document"); err != nil {
		t.Fatalf("insert category presence fixtures: %v", err)
	}

	columns := []recipe.StageColumn{
		{ID: "patient_id", Name: "patient_id"},
		{ID: "active_id", Name: "active"},
		{ID: "birth_date_id", Name: "birth_date"},
	}
	makeOutput := func(name, stageID string, operator recipe.FilterOperator) recipe.Output {
		filter := &recipe.ConstructionFilter{ColumnID: "active_id", Operator: operator}
		if operator == recipe.FilterEquals {
			active := true
			filter.Values = []recipe.FilterValue{{Kind: recipe.FilterBoolean, Boolean: &active}}
		}
		return recipe.Output{
			Name: name, RootResourceType: "Patient", RowGrain: "patient",
			RootColumnNaming: recipe.RootColumnNamingExact,
			Fields: []recipe.Field{
				{Name: "patient_id", ColumnID: "patient_id", Expr: recipe.Expression{Select: "root.id"}},
				{Name: "active", ColumnID: "active_id", Expr: recipe.Expression{Select: "root.active"}},
				{Name: "birth_date", ColumnID: "birth_date_id", Expr: recipe.Expression{Select: "root.birthDate"}},
			},
			Construction: &recipe.Construction{
				Version: 1, SourceColumns: columns,
				Steps: []recipe.ConstructionStep{{
					ID: stageID, Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
					Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: filter},
					Outputs:   columns,
				}},
			},
		}
	}
	compileScan := func(output recipe.Output, stageID string) CompiledCategoryScanQuery {
		t.Helper()
		bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
		bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
		plan, err := semantic.BuildRecipePlan(bundle, bindings)
		if err != nil {
			t.Fatal(err)
		}
		resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
		if err != nil {
			t.Fatal(err)
		}
		compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		categoryScan, err := CompileCategoryScanStageWithPolicy(compiled.Outputs[0], stageID, "birth_date_id", "patient_id", 10, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return categoryScan
	}

	assertMissingAndNull := func(categoryScan CompiledCategoryScanQuery) {
		t.Helper()
		rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: categoryScan.Query, BindVars: categoryScan.BindVars})
		if len(rows) != 2 {
			t.Fatalf("category scan returned %d categories, want distinct MISSING and NULL: %#v", len(rows), rows)
		}
		seen := map[bool]int{}
		for _, row := range rows {
			present, ok := row[categoryScan.PresentColumn].(bool)
			if !ok {
				t.Fatalf("category presence = %#v, want bool", row[categoryScan.PresentColumn])
			}
			if value, exists := row[categoryScan.ValueColumn]; !exists || value != nil {
				t.Fatalf("category value = %#v (present=%t), want explicit null payload", value, present)
			}
			seen[present]++
		}
		if seen[false] != 1 || seen[true] != 1 {
			t.Fatalf("category presence groups = %#v, want one MISSING and one NULL", seen)
		}
	}

	equalityScan := compileScan(makeOutput("construction_category_presence_equality", "keep_active_equality", recipe.FilterEquals), "keep_active_equality")
	rootFilter := "FILTER root.payload.active == @construction_filter_value"
	stageFilter := "FILTER __loom_construction_input_1.active == @construction_filter_value"
	if !strings.Contains(equalityScan.Query, rootFilter) || !strings.Contains(equalityScan.Query, stageFilter) {
		t.Fatalf("eligible equality should be copied to the root and retained on its stage:\n%s", equalityScan.Query)
	}
	if strings.Contains(equalityScan.Query, "MERGE(") || !strings.Contains(equalityScan.Query, "__loom_category_scan_presence") {
		t.Fatalf("category scan should carry presence in a scalar marker instead of merging row objects:\n%s", equalityScan.Query)
	}
	assertMissingAndNull(equalityScan)

	existsScan := compileScan(makeOutput("construction_category_presence_exists", "keep_active_exists", recipe.FilterExists), "keep_active_exists")
	if strings.Contains(existsScan.Query, "FILTER root.payload.active") || !strings.Contains(existsScan.Query, "FILTER __loom_construction_input_1.active != null") {
		t.Fatalf("ineligible EXISTS scan must keep its original staged filter without source pushdown:\n%s", existsScan.Query)
	}
	assertMissingAndNull(existsScan)
}
