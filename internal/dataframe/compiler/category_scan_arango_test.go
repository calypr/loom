package compiler

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
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

func TestRootCategoryScanUsesSharedTypedIndexContractAgainstArango(t *testing.T) {
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
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project, generation := "loom_category_root_type_"+uuid.NewString(), "generation-root-type"
	const allowedPath = "/category/allowed"
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER STARTS_WITH(document._key, @keyPrefix) REMOVE document IN Observation",
			map[string]any{"keyPrefix": project + "_"},
		); err != nil {
			t.Errorf("remove typed root category fixtures: %v", err)
		}
	}()

	type fixture struct {
		id, resourceType, authPath string
		quantity                   any
		includeQuantity            bool
		project, generation        string
	}
	fixtures := []fixture{
		{id: "missing", resourceType: "Observation", authPath: allowedPath, project: project, generation: generation},
		{id: "null", resourceType: "Observation", authPath: allowedPath, project: project, generation: generation, includeQuantity: true, quantity: map[string]any{"code": nil}},
		{id: "string", resourceType: "Observation", authPath: allowedPath, project: project, generation: generation, includeQuantity: true, quantity: map[string]any{"code": "d"}},
		{id: "string-duplicate", resourceType: "Observation", authPath: allowedPath, project: project, generation: generation, includeQuantity: true, quantity: map[string]any{"code": "d"}},
		{id: "other-project", resourceType: "Observation", authPath: allowedPath, project: project + "_foreign", generation: generation, includeQuantity: true, quantity: map[string]any{"code": "foreign-project"}},
		{id: "other-generation", resourceType: "Observation", authPath: allowedPath, project: project, generation: generation + "_foreign", includeQuantity: true, quantity: map[string]any{"code": "foreign-generation"}},
		{id: "denied-scope", resourceType: "Observation", authPath: "/category/denied", project: project, generation: generation, includeQuantity: true, quantity: map[string]any{"code": "denied"}},
		// ResourceType mismatches cannot be produced by the supported FHIR
		// ingest path; this guard makes the shared type-ordered index explicit.
		{id: "wrong-type", resourceType: "Patient", authPath: allowedPath, project: project, generation: generation, includeQuantity: true, quantity: map[string]any{"code": "wrong-type"}},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, fixture := range fixtures {
		payload := map[string]any{"resourceType": fixture.resourceType, "id": fixture.id}
		if fixture.includeQuantity {
			payload["valueQuantity"] = fixture.quantity
		}
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + fixture.id, "id": fixture.id, "project": fixture.project,
			"project_id": fixture.project, "dataset_generation": fixture.generation,
			"resourceType": fixture.resourceType, "auth_resource_path": fixture.authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert typed root category fixtures: %v", err)
	}

	columns := []recipe.StageColumn{
		{ID: "observation_id", Name: "observation_id"},
		{ID: "category_id", Name: "category"},
		{ID: "value_id", Name: "value"},
	}
	output := recipe.Output{
		Name: "root_category_shared_index", RootResourceType: "Observation", RowGrain: "observation",
		RootColumnNaming: recipe.RootColumnNamingExact,
		Fields: []recipe.Field{
			{Name: "observation_id", ColumnID: "observation_id", Expr: recipe.Expression{Select: "root.id"}},
			{Name: "category", ColumnID: "category_id", Expr: recipe.Expression{Select: "root.valueQuantity.code"}},
			{Name: "value", ColumnID: "value_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{Version: 1, SourceColumns: columns},
	}
	bindings := recipe.RuntimeBindings{
		Project: project, SelectionProject: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{allowedPath},
	}
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
	scan, err := CompileCategoryScanStageWithPolicy(compiled.Outputs[0], recipe.ConstructionSourceProjectionID, "category_id", "value_id", 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	wantIndexFields := []string{"project", "dataset_generation", "resourceType", "payload.valueQuantity.code", "auth_resource_path"}
	if scan.CategoryIndex == nil || scan.CategoryIndex.Name != previewCoveringIndexName("Observation", wantIndexFields) ||
		!sameCategoryIndexFields(scan.CategoryIndex.Fields, wantIndexFields) || scan.CategoryIndex.Supersedes == nil {
		t.Fatalf("root category index does not share the related-category contract: %+v", scan.CategoryIndex)
	}
	if !strings.Contains(scan.Query, "root.resourceType == @__loom_category_resource_type") ||
		scan.BindVars["__loom_category_resource_type"] != "Observation" {
		t.Fatalf("root category query omitted exact resource type guard: %s\n%#v", scan.Query, scan.BindVars)
	}

	rows := executeReshapeOracleQuery(t, ctx, client, CompiledQuery{Query: scan.Query, BindVars: scan.BindVars})
	seen := map[string]bool{}
	for _, row := range rows {
		present, ok := row[scan.PresentColumn].(bool)
		if !ok {
			t.Fatalf("category presence = %#v, want bool", row[scan.PresentColumn])
		}
		value, exists := row[scan.ValueColumn]
		if !exists {
			t.Fatalf("category row omitted value column: %#v", row)
		}
		key := "missing"
		if present {
			if value == nil {
				key = "null"
			} else if value == "d" {
				key = "string:d"
			} else {
				t.Fatalf("unexpected scoped category: present=%t value=%#v", present, value)
			}
		} else if value != nil {
			t.Fatalf("missing category has value %#v, want null placeholder", value)
		}
		seen[key] = true
	}
	if len(rows) != 3 || !seen["missing"] || !seen["null"] || !seen["string:d"] {
		t.Fatalf("typed source categories = %#v, want exactly MISSING, NULL, and string d", rows)
	}
}
