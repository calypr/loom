package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/render/aql"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
	"github.com/calypr/loom/internal/testkit/arangotest"
)

func TestConstructionPivotSourceProjectionLivenessAgainstBindOnlyArango(t *testing.T) {
	endpoint := arangotest.Shared(t).URL

	const project, generation = "pivot-source-liveness", "pivot-source-generation"
	output := pivotSourceLivenessOutput()
	bundle := recipe.Bundle{
		RecipeSchemaVersion: recipe.CurrentSchemaVersion,
		Name:                output.Name,
		TranslationVersion:  "test",
		Outputs:             []recipe.Output{output},
	}
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := CompileResolvedRecipePlanWithPolicy(resolved, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 1 {
		t.Fatalf("compiled outputs = %d, want one", len(queries))
	}
	query := queries[0]
	if query.PhysicalPlan.StageSequence == nil || query.PhysicalPlan.StageSequence.Stages[0].GroupedPivot == nil {
		t.Fatal("compiled query does not contain a source Pivot stage")
	}
	planBeforeRender := ir.ClonePhysicalPlan(query.PhysicalPlan)
	if _, err := aql.RenderPhysicalPlan(query.PhysicalPlan); err != nil {
		t.Fatalf("render compiled Pivot plan: %v", err)
	}
	if !reflect.DeepEqual(query.PhysicalPlan, planBeforeRender) {
		t.Fatal("rendering the Pivot plan mutated its caller-owned plan")
	}

	records := []map[string]any{
		pivotSourceLivenessRecord("a-female-1", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 2}),
		pivotSourceLivenessRecord("a-female-2", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 4}),
		pivotSourceLivenessRecord("a-male", true, "allowed", map[string]any{"gender": "male", "multipleBirthInteger": 3}),
		pivotSourceLivenessRecord("a-null", true, "allowed", map[string]any{"gender": nil, "multipleBirthInteger": 5}),
		pivotSourceLivenessRecord("a-missing", true, "allowed", map[string]any{"multipleBirthInteger": 7}),
		pivotSourceLivenessRecord("b-female", false, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 10}),
		pivotSourceLivenessRecord("b-null", false, "allowed", map[string]any{"gender": nil, "multipleBirthInteger": 6}),
		pivotSourceLivenessRecord("b-missing", false, "allowed", map[string]any{"multipleBirthInteger": 8}),
	}
	foreignProject := pivotSourceLivenessRecord("foreign-project", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	foreignProject["project"], foreignProject["project_id"] = "other-project", "other-project"
	records = append(records, foreignProject)
	foreignGeneration := pivotSourceLivenessRecord("foreign-generation", true, "allowed", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	foreignGeneration["dataset_generation"] = "old-generation"
	records = append(records, foreignGeneration)
	foreignAuthorization := pivotSourceLivenessRecord("foreign-auth", true, "denied", map[string]any{"gender": "female", "multipleBirthInteger": 99})
	records = append(records, foreignAuthorization)

	queryText := strings.Replace(query.Query, "FOR root IN @@root_collection", "FOR root IN @records", 1)
	if queryText == query.Query {
		t.Fatal("compiled query did not contain its direct root collection scan")
	}
	delete(query.BindVars, "@root_collection")
	query.BindVars["records"] = records
	query.BindVars["auth_resource_paths_unrestricted"] = false
	query.BindVars["auth_resource_paths"] = []string{"allowed"}
	rows := runPivotSourceLivenessBindQuery(t, endpoint, queryText, query.BindVars)
	for index := range rows {
		projected := make(map[string]any, 5)
		for _, name := range []string{"group", "female_amount", "male_amount", "null_amount", "missing_amount"} {
			projected[name] = rows[index][name]
		}
		rows[index] = projected
	}
	sort.Slice(rows, func(i, j int) bool { return fmt.Sprint(rows[i]["group"]) < fmt.Sprint(rows[j]["group"]) })
	want := []map[string]any{
		{"group": false, "female_amount": float64(10), "male_amount": nil, "null_amount": float64(6), "missing_amount": float64(8)},
		{"group": true, "female_amount": float64(6), "male_amount": float64(3), "null_amount": float64(5), "missing_amount": float64(7)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("Pivot rows = %#v, want %#v", rows, want)
	}
}

func pivotSourceLivenessOutput() recipe.Output {
	output := constructionPreviewPivotOutput("root.status", false)
	output.Name = "pivot_source_projection_liveness"
	output.Fields[0] = recipe.Field{Name: "group", ColumnID: "group_id", Expr: recipe.Expression{Select: "root.active"}}
	output.Construction.SourceColumns[0] = recipe.StageColumn{ID: "group_id", Name: "group"}

	female, male := "female", "male"
	pivotStep := &output.Construction.Steps[0]
	pivot := pivotStep.Operation.Pivot
	pivot.GroupKeyIDs = []string{"group_id"}
	pivot.DuplicatePolicy = recipe.PivotDuplicateSum
	pivot.UnlistedCategoryPolicy = recipe.PivotUnlistedCategoryError
	pivot.Categories = []recipe.ConstructionPivotCategory{
		{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &female}, OutputColumnID: "female_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarString, String: &male}, OutputColumnID: "male_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarNull}, OutputColumnID: "null_amount_id"},
		{Key: recipe.TableScalar{Kind: recipe.TableScalarMissing}, OutputColumnID: "missing_amount_id"},
	}
	pivotStep.Outputs = []recipe.StageColumn{
		{ID: "group_id", Name: "group"},
		{ID: "female_amount_id", Name: "female_amount"},
		{ID: "male_amount_id", Name: "male_amount"},
		{ID: "null_amount_id", Name: "null_amount"},
		{ID: "missing_amount_id", Name: "missing_amount"},
	}
	for index := range 50 {
		name := fmt.Sprintf("unused_source_%d", index)
		columnID := name + "_id"
		output.Fields = append(output.Fields, recipe.Field{Name: name, ColumnID: columnID, Expr: recipe.Expression{Select: "root.active"}})
		output.Construction.SourceColumns = append(output.Construction.SourceColumns, recipe.StageColumn{ID: columnID, Name: name})
	}
	return output
}

func pivotSourceLivenessRecord(id string, group bool, authPath string, payload map[string]any) map[string]any {
	resource := map[string]any{"id": id, "resourceType": "Patient", "active": group}
	for _, field := range []string{"multipleBirthInteger", "gender"} {
		if value, exists := payload[field]; exists {
			resource[field] = value
		}
	}
	return map[string]any{
		"_key": id, "id": id, "project": "pivot-source-liveness", "project_id": "pivot-source-liveness",
		"dataset_generation": "pivot-source-generation", "resourceType": "Patient", "auth_resource_path": authPath,
		"payload": resource,
	}
}

func runPivotSourceLivenessBindQuery(t *testing.T, endpoint, query string, bindVars map[string]any) []map[string]any {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"query": query, "bindVars": bindVars,
		"options": map[string]any{"maxRuntime": 8}, "memoryLimit": 268435456,
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 12 * time.Second}
	response, err := client.Post(strings.TrimRight(endpoint, "/")+"/_api/cursor", "application/json", bytes.NewReader(body))
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
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("decode Arango response (HTTP %d): %v", response.StatusCode, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Error || result.HasMore {
		t.Fatalf("bind-only Pivot query failed or truncated (HTTP %d): %+v", response.StatusCode, result)
	}
	return result.Rows
}
