package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
)

func TestNestedExtensionColumnsKeepAncestorOwnersAgainstArango(t *testing.T) {
	endpoint := os.Getenv("LOOM_SELECTOR_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_SELECTOR_ARANGO_URL to an existing Arango database URL")
	}
	parentA, parentB := "https://example.org/parent-a", "https://example.org/parent-b"
	leaf := "https://example.org/shared-leaf"
	columns := []recipe.ExtensionColumnMapping{
		{Name: "left", URL: leaf, URLPath: []string{parentA}, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string"},
		{Name: "right", URL: leaf, URLPath: []string{parentB}, SourcePath: "extension[].extension[]", ValuePath: "valueString", ValueType: "string"},
	}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: "extension-ancestry", TranslationVersion: "test", Outputs: []recipe.Output{{
		Name: "Patient", RootResourceType: "Patient", RowGrain: "patient",
		ExtensionColumns: []recipe.ExtensionColumn{{Name: "extensions", ColumnPrefix: extensionStringPointer(""), Source: recipe.Expression{Select: "root.extension[]"}, Columns: columns, MaxColumns: 4}},
	}}}
	plan, err := recipeplan.BuildRecipePlan(bundle, recipe.RuntimeBindings{Project: "extension-ancestry", DatasetGeneration: "generation"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, "scope", "generation")
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := aql.Render(compiled.Outputs[0].Plan, aql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	query := strings.Replace(rendered.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
	if query == rendered.Query {
		t.Fatal("root collection substitution did not apply")
	}
	delete(rendered.BindVars, "@root_collection")
	rendered.BindVars["records"] = []any{map[string]any{
		"_key": "patient-1", "project": "extension-ancestry", "dataset_generation": "generation",
		"payload": map[string]any{"resourceType": "Patient", "id": "patient-1", "extension": []any{
			map[string]any{"url": parentA, "extension": []any{map[string]any{"url": leaf, "valueString": "left-owned"}}},
			map[string]any{"url": parentB, "extension": []any{map[string]any{"url": leaf, "valueString": "right-owned"}}},
		}},
	}}
	body, err := json.Marshal(map[string]any{"query": query, "bindVars": rendered.BindVars, "batchSize": 100})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(endpoint, "/")+"/_api/cursor", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result struct {
		Error   bool             `json:"error"`
		HasMore bool             `json:"hasMore"`
		Rows    []map[string]any `json:"result"`
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Error || result.HasMore || len(result.Rows) != 1 {
		t.Fatalf("status=%d response=%+v", response.StatusCode, result)
	}
	want := map[string]any{"left": "left-owned", "right": "right-owned"}
	for key, value := range want {
		if !reflect.DeepEqual(result.Rows[0][key], value) {
			t.Fatalf("%s=%#v want=%#v", key, result.Rows[0][key], value)
		}
	}
}

func extensionStringPointer(value string) *string { return &value }
