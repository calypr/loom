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
	"github.com/calypr/loom/internal/dataframe/expression"
	"github.com/calypr/loom/internal/dataframe/recipe"
)

func TestColumnPopulationMatchesExactArangoStageRows(t *testing.T) {
	endpoint := os.Getenv("LOOM_SELECTOR_ARANGO_URL")
	if endpoint == "" {
		t.Skip("set LOOM_SELECTOR_ARANGO_URL")
	}

	fields := []recipe.Field{
		{Name: "amount", ColumnID: "amount", Expr: recipe.Expression{Select: "root.valueInteger"}},
		{Name: "flag", ColumnID: "flag", Expr: recipe.Expression{Select: "root.valueBoolean"}},
		{Name: "text", ColumnID: "text", Expr: recipe.Expression{Select: "root.status"}},
	}
	columns := make([]recipe.StageColumn, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, recipe.StageColumn{ID: field.ColumnID, Name: field.Name})
	}
	threshold := int64(1000)
	output := compilePopulationMappingOutput(t, recipe.Output{
		Name: "Population", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact, Fields: fields,
		Construction: &recipe.Construction{
			Version: 1, SourceColumns: columns,
			Steps: []recipe.ConstructionStep{{
				ID: "drop_large", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionFilterOp, Filter: &recipe.ConstructionFilter{
					ColumnID: "amount", Operator: recipe.FilterGreaterThan,
					Values: []recipe.FilterValue{{Kind: recipe.FilterInteger, Integer: &threshold}},
				}},
				Outputs: columns,
			}},
		},
	})
	if len(output.Stages) == 0 {
		t.Fatal("compiled output has no construction stages")
	}
	if output.OutputSchema[0].Kind != string(expression.KindInteger) {
		t.Fatalf("integer fixture compiled as kind %q", output.OutputSchema[0].Kind)
	}

	compile := func(stage string, ids ...string) CompiledColumnPopulationScan {
		t.Helper()
		compiled, err := CompileColumnPopulationScan(output, stage, ids, ir.DefaultPhysicalOptimizationPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return compiled
	}
	execute := func(compiled CompiledColumnPopulationScan, records []map[string]any) []map[string]any {
		t.Helper()
		query := strings.Replace(compiled.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
		if query == compiled.Query {
			t.Fatal("compiled population query has no root scan")
		}
		binds := make(map[string]any, len(compiled.BindVars)+1)
		for key, value := range compiled.BindVars {
			binds[key] = value
		}
		delete(binds, "@root_collection")
		binds["records"] = records
		binds["auth_resource_paths_unrestricted"] = false
		binds["auth_resource_paths"] = []string{"allowed"}
		body, err := json.Marshal(map[string]any{
			"query": query, "bindVars": binds, "options": map[string]any{"maxRuntime": 10},
		})
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPost, strings.TrimRight(endpoint, "/")+"/_api/cursor", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := (&http.Client{Timeout: 15 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result struct {
			Error    bool             `json:"error"`
			ErrorNum int              `json:"errorNum"`
			Message  string           `json:"errorMessage"`
			Code     int              `json:"code"`
			HasMore  bool             `json:"hasMore"`
			Rows     []map[string]any `json:"result"`
		}
		if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 || result.Error || result.HasMore {
			t.Fatalf("Arango query failed or was incomplete (HTTP %d): %+v\n%s", response.StatusCode, result, query)
		}
		if len(result.Rows) != 1 {
			t.Fatalf("population aggregate returned %d rows, want exactly one: %+v\n%s", len(result.Rows), result.Rows, query)
		}
		return result.Rows
	}
	root := func(_ CompiledColumnPopulationScan, key string, payload map[string]any, project, generation any, auth string) map[string]any {
		return map[string]any{
			"_key": key, "project": project, "dataset_generation": generation,
			"auth_resource_path": auth, "payload": payload,
		}
	}
	base := compile(recipe.ConstructionSourceProjectionID, "amount", "flag", "text")
	project, generation := base.BindVars["project"], base.BindVars["dataset_generation"]
	records := make([]map[string]any, 0, 54)
	for index := 0; index < 50; index++ {
		payload := map[string]any{}
		if index%2 == 0 {
			payload["valueInteger"] = nil
		}
		records = append(records, root(base, fmt.Sprintf("null_%02d", index), payload, project, generation, "allowed"))
	}
	records = append(records, root(base, "falsey_values", map[string]any{
		"valueInteger": 0, "valueBoolean": false, "status": "",
	}, project, generation, "allowed"))
	// The source and stage projections see all records, including the 51st
	// value. A preview-shaped LIMIT 50 would incorrectly report amount empty.
	got := execute(base, records)[0]
	want := map[string]any{
		"populated_0": true, "populated_1": true, "populated_2": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("source population statuses = %#v, want %#v", got, want)
	}

	zeroRows := execute(base, []map[string]any{})[0]
	for index := range base.ColumnIDs {
		if populated, ok := zeroRows[fmt.Sprintf("populated_%d", index)].(bool); !ok || populated {
			t.Fatalf("zero-row source must report every field empty; row=%#v", zeroRows)
		}
	}

	emptyOnly := []map[string]any{
		root(base, "null", map[string]any{"valueInteger": nil}, project, generation, "allowed"),
		root(base, "absent", map[string]any{}, project, generation, "allowed"),
	}
	if empty := execute(compile(recipe.ConstructionSourceProjectionID, "amount"), emptyOnly)[0]["populated_0"]; empty != false {
		t.Fatalf("null and absent values should be empty, got %#v", empty)
	}

	wrongScopeOnly := []map[string]any{
		root(base, "wrong_project", map[string]any{"status": "blocked"}, "other-project", generation, "allowed"),
		root(base, "wrong_generation", map[string]any{"status": "blocked"}, project, "old-generation", "allowed"),
		root(base, "wrong_auth", map[string]any{"status": "blocked"}, project, generation, "forbidden"),
	}
	if populated := execute(compile(recipe.ConstructionSourceProjectionID, "text"), wrongScopeOnly)[0]["populated_0"]; populated != false {
		t.Fatalf("wrong project/generation/authorization records contributed: %#v", populated)
	}

	filtered := compile("drop_large", "amount", "flag", "text")
	filteredRows := execute(filtered, records)[0]
	for index := range filtered.ColumnIDs {
		if populated, ok := filteredRows[fmt.Sprintf("populated_%d", index)].(bool); !ok || populated {
			t.Fatalf("filter-empty stage must report every field empty; row=%#v", filteredRows)
		}
	}
}
