package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestCategoryPairProjectionPreservesPresenceScopeAndOverflowAgainstArango(t *testing.T) {
	endpoint := os.Getenv("LOOM_SELECTOR_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_SELECTOR_ARANGO_URL")
	}
	output := compilePopulationMappingOutput(t, recipe.Output{Name: "Observations", RootResourceType: "Observation", RowGrain: "observation", Fields: []recipe.Field{
		{Name: "category", ColumnID: "category", Expr: recipe.Expression{Select: "root.valueInteger"}},
		{Name: "value", ColumnID: "value", Expr: recipe.Expression{Select: "root.valueBoolean"}},
		{Name: "unrelated", ColumnID: "unrelated", Expr: recipe.Expression{Select: "root.status"}},
	}, Construction: &recipe.Construction{Version: 1, SourceColumns: []recipe.StageColumn{{ID: "category", Name: "category"}, {ID: "value", Name: "value"}, {ID: "unrelated", Name: "unrelated"}}}})
	payloads := []map[string]any{
		{"valueBoolean": true}, {"valueInteger": nil, "valueBoolean": true}, {"valueInteger": 0, "valueBoolean": false},
		{"valueInteger": 1}, {"valueInteger": 2, "valueBoolean": nil}, {"valueInteger": 3, "valueBoolean": true},
	}
	execute := func(query CompiledCategoryScanQuery) []map[string]any {
		t.Helper()
		records := make([]map[string]any, 0, len(payloads)+3)
		for i, payload := range payloads {
			records = append(records, map[string]any{"_key": fmt.Sprint(i), "project": query.BindVars["project"], "dataset_generation": query.BindVars["dataset_generation"], "auth_resource_path": "allowed", "payload": payload})
		}
		for _, guard := range []string{"project", "dataset_generation", "auth_resource_path"} {
			row := map[string]any{"_key": guard, "project": query.BindVars["project"], "dataset_generation": query.BindVars["dataset_generation"], "auth_resource_path": "allowed", "payload": map[string]any{"valueInteger": 99, "valueBoolean": true}}
			row[guard] = "unauthorized"
			records = append(records, row)
		}
		query.BindVars["auth_resource_paths_unrestricted"] = false
		query.BindVars["auth_resource_paths"] = []string{"allowed"}
		text := strings.Replace(query.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
		if text == query.Query {
			t.Fatal("missing root scan")
		}
		delete(query.BindVars, "@root_collection")
		query.BindVars["records"] = records
		raw, err := json.Marshal(map[string]any{"query": text, "bindVars": query.BindVars, "options": map[string]any{"maxRuntime": 10}})
		if err != nil {
			t.Fatal(err)
		}
		response, err := (&http.Client{Timeout: 15 * time.Second}).Post(strings.TrimRight(endpoint, "/")+"/_api/cursor", "application/json", bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Error   bool             `json:"error"`
			Message string           `json:"errorMessage"`
			HasMore bool             `json:"hasMore"`
			Rows    []map[string]any `json:"result"`
		}
		if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if result.Error || result.HasMore {
			t.Fatalf("query failed or truncated: %+v", result)
		}
		return result.Rows
	}
	for _, limit := range []int{256, 2} {
		before, err := CompileCategoryScanOutputRequiringValue(output, "category", "value", limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		after, err := CompileCategoryScanStageRequiringValue(output, recipe.ConstructionSourceProjectionID, "category", "value", limit, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		full, narrow := execute(before), execute(after)
		if !reflect.DeepEqual(full, narrow) {
			t.Fatalf("different category values: full=%+v narrowed=%+v", full, narrow)
		}
		want := []map[string]any{{"present": false, "value": nil}, {"present": true, "value": nil}, {"present": true, "value": float64(0)}, {"present": true, "value": float64(3)}}
		if limit == 2 {
			want = want[:3]
		}
		if !reflect.DeepEqual(narrow, want) {
			t.Fatalf("presence/scope/overflow changed: got=%+v want=%+v", narrow, want)
		}
	}
}
