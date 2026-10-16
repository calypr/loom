package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
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

func TestConstructionGroupSummaryRowsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_construction_group_summary_"+uuid.NewString(), "generation-group-summary"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "summary-a", "status": "active", "valueQuantity": map[string]any{"value": 2.0}},
		{"id": "summary-b", "status": "active", "valueQuantity": map[string]any{"value": nil}},
		{"id": "summary-c", "status": "inactive", "valueQuantity": map[string]any{"value": 4.0}},
		{"id": "summary-d", "valueQuantity": map[string]any{"value": nil}},
	})

	rows := executeConstructionOutput(t, ctx, client, constructionSummaryOutput(), project, generation)
	if len(rows) != 1 {
		t.Fatalf("whole-table summary rows = %#v, want one row", rows)
	}
	row := rows[0]
	for name, want := range map[string]float64{"rows": 4, "status_count": 3, "status_distinct": 2, "amount_sum": 6, "amount_mean": 3} {
		if !constructionNumericEqual(row[name], want) {
			t.Errorf("summary %s = %#v, want %v", name, row[name], want)
		}
	}
	if identity, ok := row["__loom_row_id"].(string); !ok || identity == "" {
		t.Fatalf("summary row identity = %#v, want stable identity", row["__loom_row_id"])
	}

	emptyProject := "loom_construction_group_empty_" + uuid.NewString()
	emptyRows := executeConstructionOutput(t, ctx, client, constructionSummaryOutput(), emptyProject, generation)
	if len(emptyRows) != 1 {
		t.Fatalf("empty whole-table summary rows = %#v, want one zero-count row", emptyRows)
	}
	for _, name := range []string{"rows", "status_count", "status_distinct"} {
		if !constructionNumericEqual(emptyRows[0][name], 0) {
			t.Errorf("empty summary %s = %#v, want 0", name, emptyRows[0][name])
		}
	}
	for _, name := range []string{"amount_sum", "amount_mean"} {
		if emptyRows[0][name] != nil {
			t.Errorf("empty summary %s = %#v, want null", name, emptyRows[0][name])
		}
	}
}

func TestConstructionGroupCountRowsOnlyKeylessEmptyAndNonemptyAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_construction_group_count_only_"+uuid.NewString(), "generation-group-count-only"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "count-only-a", "status": "active"},
		{"id": "count-only-b", "status": "active"},
		{"id": "count-only-c", "status": "inactive"},
	})

	rows := executeConstructionOutput(t, ctx, client, constructionCountRowsOnlyOutput(), project, generation)
	if len(rows) != 1 || !constructionNumericEqual(rows[0]["rows"], 3) {
		t.Fatalf("keyless count rows = %#v, want one row with count 3", rows)
	}

	emptyProject := "loom_construction_group_count_only_empty_" + uuid.NewString()
	emptyRows := executeConstructionOutput(t, ctx, client, constructionCountRowsOnlyOutput(), emptyProject, generation)
	if len(emptyRows) != 1 || !constructionNumericEqual(emptyRows[0]["rows"], 0) {
		t.Fatalf("empty keyless count rows = %#v, want one row with count 0", emptyRows)
	}
}

func TestConstructionGroupRowLineagePagesScopedContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, foreignProject, generation := "loom_group_lineage_"+uuid.NewString(), "loom_group_lineage_foreign_"+uuid.NewString(), "generation-group-lineage"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project IN @projects REMOVE document IN Observation",
			map[string]any{"projects": []string{project, foreignProject}},
		); err != nil {
			t.Errorf("remove Group row-lineage fixtures: %v", err)
		}
	})

	type fixture struct {
		project, generation, id, authPath string
	}
	fixtures := []fixture{
		{project: project, generation: generation, id: "active-a", authPath: "Observation/visible"},
		{project: project, generation: generation, id: "active-b", authPath: "Observation/visible"},
		{project: project, generation: generation, id: "active-denied", authPath: "Observation/hidden"},
		{project: project, generation: "old-generation", id: "active-old", authPath: "Observation/visible"},
		{project: foreignProject, generation: generation, id: "active-foreign", authPath: "Observation/visible"},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, item := range fixtures {
		document, err := json.Marshal(map[string]any{
			"_key": item.project + "_" + item.id, "id": item.id,
			"project": item.project, "project_id": item.project, "dataset_generation": item.generation,
			"resourceType": "Observation", "auth_resource_path": item.authPath,
			"payload": map[string]any{"id": item.id, "resourceType": "Observation", "status": "active"},
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert Group row-lineage fixtures: %v", err)
	}

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/visible"},
	}
	output := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), bindings)
	preview, err := CompileRecipeOutputWithPolicy(output, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(rows) != 1 || rows[0]["status"] != "active" || !constructionNumericEqual(rows[0]["rows"], 2) {
		t.Fatalf("authorized grouped rows = %#v, want active with exactly the two visible current-generation records", rows)
	}
	rowID, ok := rows[0]["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("Group preview row identity = %#v", rows[0]["__loom_row_id"])
	}

	compilePage := func(rowID string, offset int) CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := CompileRowLineageOutput(output, rowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile Group row-lineage page at %d: %v", offset, compileErr)
		}
		for _, want := range []string{
			"root.project == @project", "root.dataset_generation == @dataset_generation",
			"root.auth_resource_path IN @auth_resource_paths",
		} {
			if !strings.Contains(query.Query, want) {
				t.Errorf("Group row-lineage query lost source scope %q:\n%s", want, query.Query)
			}
		}
		return query
	}
	first := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, 0))
	firstContributors, _ := first["contributors"].([]any)
	if first["found"] != true || first["hasMore"] != true || len(firstContributors) != 1 {
		t.Fatalf("first Group lineage page = %#v, want one contributor and another page", first)
	}
	assertContributor := func(got any, id, occurrenceKey string) {
		t.Helper()
		contributor, _ := got.(map[string]any)
		if contributor["resourceType"] != "Observation" || contributor["resourceId"] != id || contributor["occurrenceKey"] != occurrenceKey {
			t.Errorf("Group lineage contributor = %#v, want Observation/%s at %s", contributor, id, occurrenceKey)
		}
	}
	assertContributor(firstContributors[0], "active-a", project+"_active-a")
	second := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, 1))
	secondContributors, _ := second["contributors"].([]any)
	if second["found"] != true || second["hasMore"] != false || len(secondContributors) != 1 {
		t.Fatalf("second Group lineage page = %#v, want the final contributor", second)
	}
	assertContributor(secondContributors[0], "active-b", project+"_active-b")
	empty := executeRowLineageOracleQuery(t, ctx, client, compilePage(rowID, 2))
	if empty["found"] != true || empty["hasMore"] != false || len(empty["contributors"].([]any)) != 0 {
		t.Fatalf("past-end Group lineage page = %#v, want found row and empty page", empty)
	}

	var identity [][]string
	if err := json.Unmarshal([]byte(rowID), &identity); err != nil || len(identity) == 0 || len(identity[0]) != 2 {
		t.Fatalf("decode Group row identity %q: %v", rowID, err)
	}
	identity[0][1] = "different_group"
	wrongStageBytes, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	wrongStageID := string(wrongStageBytes)
	for _, forgedID := range []string{"forged-group-row-id", wrongStageID} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(forgedID, 0))
		if result["found"] != false || len(result["contributors"].([]any)) != 0 {
			t.Errorf("forged/wrong-stage Group identity disclosed contributors: %#v", result)
		}
	}
}

func TestConstructionGroupFilterRowLineagePagesScopedContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, foreignProject, generation := "loom_group_filter_lineage_"+uuid.NewString(), "loom_group_filter_lineage_foreign_"+uuid.NewString(), "generation-group-filter-lineage"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project IN @projects REMOVE document IN Observation",
			map[string]any{"projects": []string{project, foreignProject}},
		); err != nil {
			t.Errorf("remove Group-filter row-lineage fixtures: %v", err)
		}
	})

	type fixture struct {
		project, generation, id, authPath, status string
	}
	fixtures := make([]fixture, 0, 214)
	for index := 0; index < 105; index++ {
		fixtures = append(fixtures, fixture{project, generation, fmt.Sprintf("active-%03d", index), "Observation/visible", "active"})
	}
	for index := 0; index < 104; index++ {
		fixtures = append(fixtures, fixture{project, generation, fmt.Sprintf("borderline-%03d", index), "Observation/visible", "borderline"})
	}
	fixtures = append(fixtures,
		fixture{project, generation, "active-denied", "Observation/hidden", "active"},
		fixture{project, "old-generation", "active-old", "Observation/visible", "active"},
		fixture{foreignProject, generation, "active-foreign", "Observation/visible", "active"},
	)
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, item := range fixtures {
		document, err := json.Marshal(map[string]any{
			"_key": item.project + "_" + item.id, "id": item.id,
			"project": item.project, "project_id": item.project, "dataset_generation": item.generation,
			"resourceType": "Observation", "auth_resource_path": item.authPath,
			"payload": map[string]any{"id": item.id, "resourceType": "Observation", "status": item.status},
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert Group-filter row-lineage fixtures: %v", err)
	}

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/visible"},
	}
	unfiltered := lowerConstructionOutput(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), bindings)
	unfilteredPreview, err := CompileRecipeOutputWithPolicy(unfiltered, bindings, 200, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	unfilteredRows := executeReshapeOracleQuery(t, ctx, client, unfilteredPreview)
	var borderlineID string
	for _, row := range unfilteredRows {
		if row["status"] == "borderline" && constructionNumericEqual(row["rows"], 104) {
			borderlineID, _ = row["__loom_row_id"].(string)
		}
	}
	if borderlineID == "" {
		t.Fatalf("unfiltered Group preview omitted the 104-row identity: %#v", unfilteredRows)
	}

	output := lowerConstructionOutput(t, constructionGroupFilterLineageOutput(104, 103), bindings)
	preview, err := CompileRecipeOutputWithPolicy(output, bindings, 200, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(rows) != 1 || rows[0]["status"] != "active" || !constructionNumericEqual(rows[0]["rows"], 105) {
		t.Fatalf("two-filter Group output = %#v, want only the authorized 105-row active group", rows)
	}
	rowID, ok := rows[0]["__loom_row_id"].(string)
	if !ok || rowID == "" {
		t.Fatalf("filtered Group row identity = %#v", rows[0]["__loom_row_id"])
	}

	compilePage := func(compiled lower.CompiledRecipeOutput, id string, offset int) CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := CompileRowLineageOutput(compiled, id, offset, 50, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile filtered Group row-lineage page at %d: %v", offset, compileErr)
		}
		for _, want := range []string{
			"root.project == @project", "root.dataset_generation == @dataset_generation",
			"root.auth_resource_path IN @auth_resource_paths", "LIMIT @row_lineage_offset, @row_lineage_fetch_limit",
		} {
			if !strings.Contains(query.Query, want) {
				t.Errorf("filtered Group row-lineage query lost required scope/page clause %q:\n%s", want, query.Query)
			}
		}
		return query
	}
	contributors := make(map[string]bool, 105)
	for pageIndex, offset := range []int{0, 50, 100} {
		page := executeRowLineageOracleQuery(t, ctx, client, compilePage(output, rowID, offset))
		items, _ := page["contributors"].([]any)
		wantLengths := []int{50, 50, 5}
		if page["found"] != true || len(items) != wantLengths[pageIndex] || page["hasMore"] != (offset < 100) {
			t.Fatalf("filtered Group lineage page at %d = %#v", offset, page)
		}
		for _, item := range items {
			contributor, _ := item.(map[string]any)
			id, _ := contributor["resourceId"].(string)
			key, _ := contributor["occurrenceKey"].(string)
			if contributor["resourceType"] != "Observation" || id == "" || key != project+"_"+id || contributors[id] {
				t.Errorf("scoped Group contributor = %#v, duplicate=%t", contributor, contributors[id])
			}
			contributors[id] = true
		}
	}
	if len(contributors) != 105 {
		t.Fatalf("paged Group contributor count = %d, want 105", len(contributors))
	}
	pastEnd := executeRowLineageOracleQuery(t, ctx, client, compilePage(output, rowID, 105))
	if pastEnd["found"] != true || pastEnd["hasMore"] != false || len(pastEnd["contributors"].([]any)) != 0 {
		t.Fatalf("past-end filtered Group lineage page = %#v, want found row and empty page", pastEnd)
	}

	var identity [][]string
	if err := json.Unmarshal([]byte(borderlineID), &identity); err != nil || len(identity) < 3 {
		t.Fatalf("decode unfiltered Group row identity %q: %v", borderlineID, err)
	}
	wrongStage := make([][]string, len(identity))
	for index := range identity {
		wrongStage[index] = append([]string(nil), identity[index]...)
	}
	wrongStage[0][1] = "other-construction"
	wrongStageBytes, err := json.Marshal(wrongStage)
	if err != nil {
		t.Fatal(err)
	}
	for _, excludedID := range []string{borderlineID, string(wrongStageBytes), "forged-group-row-id"} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(output, excludedID, 0))
		if result["found"] != false || len(result["contributors"].([]any)) != 0 {
			t.Errorf("excluded or forged Group identity disclosed contributors: %#v", result)
		}
	}

	lateFilterOutput := lowerConstructionOutput(t, constructionGroupFilterLineageOutput(103, 104), bindings)
	lateExcluded := executeRowLineageOracleQuery(t, ctx, client, compilePage(lateFilterOutput, borderlineID, 0))
	if lateExcluded["found"] != false || len(lateExcluded["contributors"].([]any)) != 0 {
		t.Fatalf("identity rejected only by the last FILTER still disclosed contributors: %#v", lateExcluded)
	}
}

func TestConstructionGroupCountRowsOnlyQueryAvoidsInputRowBuffers(t *testing.T) {
	query := compileConstructionOutputQuery(t, constructionCountRowsOnlyOutput(), "construction-count-rows-only", "generation-count-rows-only")
	if !strings.Contains(query.Query, "COLLECT WITH COUNT INTO ") {
		t.Fatalf("count-only query does not use streaming row count:\n%s", query.Query)
	}
	for _, bufferedRows := range []string{
		"construction_group_input_rows",
		"construction_group_all_rows",
		"LET __loom_construction_source_projection = (",
	} {
		if strings.Contains(query.Query, bufferedRows) {
			t.Errorf("keyless count-only query retains %q:\n%s", bufferedRows, query.Query)
		}
	}
	if strings.Contains(query.Query, "SORT root.id ASC") {
		t.Fatalf("keyless count-only query sorts source rows before counting:\n%s", query.Query)
	}
	collectAt := strings.Index(query.Query, "COLLECT WITH COUNT INTO")
	limitAt := strings.LastIndex(query.Query, "LIMIT @limit")
	if collectAt < 0 || limitAt < collectAt {
		t.Fatalf("preview bound is not applied after the exact keyless count:\n%s", query.Query)
	}
	if strings.Contains(query.Query[:collectAt], "SORT ") {
		t.Fatalf("keyless count query sorts source rows before the count:\n%s", query.Query)
	}
	if query.PartialValidation {
		t.Fatal("exact keyless count was marked as partial validation")
	}

	restricted := compileConstructionOutputQueryWithBindings(t, constructionCountRowsOnlyOutput(), "construction-count-rows-only", "generation-count-rows-only", recipe.RuntimeBindings{
		AuthScopeMode:     authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Specimen/visible"},
	})
	for _, scopedPredicate := range []string{
		"root.project == @project",
		"root.dataset_generation == @dataset_generation",
		"root.auth_resource_path IN @auth_resource_paths",
	} {
		if !strings.Contains(restricted.Query, scopedPredicate) {
			t.Errorf("inlined count source lost canonical scope predicate %q:\n%s", scopedPredicate, restricted.Query)
		}
	}
	if restricted.BindVars["auth_resource_paths_unrestricted"] != false {
		t.Fatalf("inlined count source bypassed restricted authorization: %#v", restricted.BindVars)
	}
	if paths, ok := restricted.BindVars["auth_resource_paths"].([]string); !ok || len(paths) != 1 || paths[0] != "Specimen/visible" {
		t.Fatalf("inlined count source changed the authorized paths: %#v", restricted.BindVars["auth_resource_paths"])
	}

	keyedQuery := compileConstructionOutputQuery(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyError), "construction-count-rows-only", "generation-count-rows-only")
	if !strings.Contains(keyedQuery.Query, "WITH COUNT INTO ") || !strings.Contains(keyedQuery.Query, "CONSTRUCTION_GROUP_MISSING_KEY") {
		t.Fatalf("keyed count query did not retain its missing-key error behavior:\n%s", keyedQuery.Query)
	}
	keyed := constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup)
	keyed.Fields = append(keyed.Fields, recipe.Field{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}})
	keyed.Construction.SourceColumns = append(keyed.Construction.SourceColumns, recipe.StageColumn{ID: "amount_id", Name: "amount", Type: "decimal"})
	pruned := compileConstructionOutputQuery(t, keyed, "construction-count-rows-only", "generation-count-rows-only")
	if !strings.Contains(pruned.Query, "root.payload.status") || strings.Contains(pruned.Query, "root.payload.valueQuantity.value") {
		t.Fatalf("keyed count query projected an unused source field:\n%s", pruned.Query)
	}
	if !strings.Contains(pruned.Query, "SORT __loom_construction_group_key_1 ASC") || strings.Contains(pruned.Query, "SORT __loom_construction_final_row.__loom_row_id") {
		t.Fatalf("group query did not preserve its key order without a second final sort:\n%s", pruned.Query)
	}
}

func TestConstructionGroupAllRowValuesUsesStreamingTypedAggregates(t *testing.T) {
	query := compileConstructionOutputQuery(t, constructionGroupRowValuesOutput(), "construction-group-row-values", "generation-group-row-values")
	for _, want := range []string{
		"COLLECT __loom_construction_group_key_",
		"AGGREGATE __loom_physical_construction_construction_group_count_rows = SUM(__loom_construction_input_1 != null ? 1 : 0)",
		"UNIQUE((__loom_construction_input_1[@",
		"ASSERT(IS_ARRAY(__loom_construction_input_1[@",
		"FLATTEN(__loom_physical_construction_construction_group_row_value_arrays_0, 1)",
		"CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH",
		"SORTED_UNIQUE((FOR ",
		"CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH",
	} {
		if !strings.Contains(query.Query, want) {
			t.Errorf("streaming Group query missing %q:\n%s", want, query.Query)
		}
	}
	for _, buffered := range []string{
		"construction_source_projection = (",
		"construction_group_input_rows",
		" INTO __loom_construction_group_rows_",
		"LENGTH((FOR ",
	} {
		if strings.Contains(query.Query, buffered) {
			t.Errorf("streaming Group query retained contributor materialization %q:\n%s", buffered, query.Query)
		}
	}
	projectionAt := strings.Index(query.Query, "LET __loom_construction_input_1 = {")
	collectAt := strings.Index(query.Query, "COLLECT __loom_construction_group_key_")
	if projectionAt < 0 || collectAt < projectionAt {
		t.Fatalf("direct source projection was not assigned in the Group scope before COLLECT:\n%s", query.Query)
	}
	for _, scopeClause := range []string{
		"FILTER root.project == @project",
		"FILTER root.dataset_generation == @dataset_generation",
		"FILTER root_scope_allowed == @scope_allowed",
	} {
		if !strings.Contains(query.Query, scopeClause) {
			t.Errorf("streamed Group source lost scope clause %q:\n%s", scopeClause, query.Query)
		}
	}
	if strings.Contains(query.Query, "FOR __loom_selector_") || !strings.Contains(query.Query, "[* FILTER CURRENT.text != null RETURN CURRENT.text]") {
		t.Fatalf("repeated source selector did not use a typed inline array expansion:\n%s", query.Query)
	}
	oneQuery := compileConstructionOutputQuery(t, constructionGroupOneRowValueOutput(), "construction-group-one-row-value", "generation-group-one-row-value")
	if strings.Contains(oneQuery.Query, "construction_source_projection = (") || !strings.Contains(oneQuery.Query, "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") {
		t.Fatalf("ONE row values did not use direct-source aggregation with its multiple-value check:\n%s", oneQuery.Query)
	}
}

func TestConstructionGroupStreamingKeepsExpandedInputStage(t *testing.T) {
	query := compileConstructionOutputQuery(t, constructionExpandGroupOutput(), "construction-group-expanded", "generation-group-expanded")
	if !strings.Contains(query.Query, "FOR __loom_construction_input_2 IN __loom_construction_stage_1") {
		t.Fatalf("Group after expansion no longer consumes the expanded stage:\n%s", query.Query)
	}
	if !strings.Contains(query.Query, "construction_source_projection = (") {
		t.Fatalf("multi-stage Group bypassed its staged source input:\n%s", query.Query)
	}
}

func TestConstructionGroupAllRowValuesMatchContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_construction_group_row_values_"+uuid.NewString(), "generation-group-row-values"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "values-active-a", "status": "active", "note": []any{map[string]any{"text": "blue"}, map[string]any{"text": "red"}, map[string]any{"text": "red"}, map[string]any{"text": nil}}},
		{"id": "values-active-b", "status": "active", "note": []any{map[string]any{"text": "green"}, map[string]any{"text": "red"}}},
		{"id": "values-active-empty", "status": "active", "note": []any{}},
		{"id": "values-inactive", "status": "inactive", "note": []any{map[string]any{"text": nil}}},
		{"id": "values-missing-status", "note": []any{map[string]any{"text": "orphan"}}},
	})

	rows := executeConstructionOutput(t, ctx, client, constructionGroupRowValuesOutput(), project, generation)
	byStatus := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		status := "<missing>"
		if row["status"] != nil {
			status = row["status"].(string)
		}
		byStatus[status] = row
	}
	want := map[string]struct {
		count  float64
		values []any
	}{
		"active":    {count: 3, values: []any{"blue", "green", "red"}},
		"inactive":  {count: 1, values: []any{}},
		"<missing>": {count: 1, values: []any{"orphan"}},
	}
	if len(byStatus) != len(want) {
		t.Fatalf("Group output has %d keys, want %d: %#v", len(byStatus), len(want), byStatus)
	}
	for status, expected := range want {
		row, ok := byStatus[status]
		if !ok {
			t.Fatalf("Group output omitted status %q: %#v", status, byStatus)
		}
		if !constructionNumericEqual(row["rows"], expected.count) {
			t.Errorf("status %q row count = %#v, want %v", status, row["rows"], expected.count)
		}
		if !reflect.DeepEqual(row["tag_values"], expected.values) {
			t.Errorf("status %q ALL values = %#v, want %#v", status, row["tag_values"], expected.values)
		}
	}
}

func TestConstructionGroupOneRowValuePreservesDistinctAndErrorSemanticsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	generation := "generation-group-one-row-value"
	validProject := "loom_construction_group_one_values_" + uuid.NewString()
	multipleProject := "loom_construction_group_one_multiple_" + uuid.NewString()
	invalidTypeProject := "loom_construction_group_one_invalid_type_" + uuid.NewString()
	projects := []string{validProject, multipleProject, invalidTypeProject}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, project := range projects {
			if err := client.ExecuteAQL(cleanupCtx,
				"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
				map[string]any{"project": project},
			); err != nil {
				t.Errorf("remove Group ONE fixture %q: %v", project, err)
			}
		}
	})

	insertConstructionReshapeRows(t, ctx, client, validProject, generation, []map[string]any{
		{"id": "one-active-a", "status": "active", "note": []any{map[string]any{"text": "blue"}, map[string]any{"text": "blue"}}},
		{"id": "one-active-b", "status": "active", "note": []any{map[string]any{"text": "blue"}}},
		{"id": "one-empty", "status": "empty", "note": []any{}},
		{"id": "one-null", "status": "null", "note": []any{map[string]any{"text": nil}}},
	})
	rows := executeConstructionOutput(t, ctx, client, constructionGroupOneRowValueOutput(), validProject, generation)
	byStatus := make(map[string]map[string]any, len(rows))
	for _, row := range rows {
		byStatus[row["status"].(string)] = row
	}
	for status, want := range map[string]struct {
		count float64
		value any
	}{
		"active": {count: 2, value: "blue"},
		"empty":  {count: 1, value: nil},
		"null":   {count: 1, value: nil},
	} {
		row, ok := byStatus[status]
		if !ok {
			t.Fatalf("Group ONE omitted status %q: %#v", status, byStatus)
		}
		if !constructionNumericEqual(row["rows"], want.count) || !reflect.DeepEqual(row["tag_value"], want.value) {
			t.Errorf("Group ONE status %q = %#v, want rows=%v value=%#v", status, row, want.count, want.value)
		}
	}

	insertConstructionReshapeRows(t, ctx, client, multipleProject, generation, []map[string]any{
		{"id": "one-multiple-a", "status": "active", "note": []any{map[string]any{"text": "blue"}}},
		{"id": "one-multiple-b", "status": "active", "note": []any{map[string]any{"text": "red"}}},
	})
	if err := executeConstructionOutputError(t, ctx, client, constructionGroupOneRowValueOutput(), multipleProject, generation); err == nil || !strings.Contains(err.Error(), "CONSTRUCTION_ROW_VALUE_MULTIPLE_VALUES") {
		t.Fatalf("Group ONE accepted multiple distinct values, error = %v", err)
	}

	insertConstructionReshapeRows(t, ctx, client, invalidTypeProject, generation, []map[string]any{
		{"id": "one-invalid-type", "status": "active", "note": []any{map[string]any{"text": 7}}},
	})
	if err := executeConstructionOutputError(t, ctx, client, constructionGroupOneRowValueOutput(), invalidTypeProject, generation); err == nil || !strings.Contains(err.Error(), "CONSTRUCTION_ROW_VALUE_TYPE_MISMATCH") {
		t.Fatalf("Group ONE accepted an invalid value type, error = %v", err)
	}
}

func TestConstructionExpandPreserveAndGroupNullKeyRowsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_construction_expand_group_"+uuid.NewString(), "generation-expand-group"
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "tags-a", "status": "active", "note": []any{map[string]any{"text": "red"}, map[string]any{"text": "blue"}}},
		{"id": "tags-b", "status": "active", "note": []any{map[string]any{"text": "red"}}},
		{"id": "tags-c", "status": "inactive", "note": []any{}},
		{"id": "tags-d", "note": []any{}},
	})

	expanded := executeConstructionOutput(t, ctx, client, constructionExpandOutput(), project, generation)
	if len(expanded) != 5 {
		t.Fatalf("preserved list expansion rows = %#v, want five rows", expanded)
	}
	ordinalBySourceAndTag := map[string]float64{}
	identities := make(map[string]bool, len(expanded))
	for _, row := range expanded {
		identity, ok := row["__loom_row_id"].(string)
		if !ok || identity == "" || identities[identity] {
			t.Errorf("expanded row identity is missing or duplicated: %#v", row["__loom_row_id"])
		}
		identities[identity] = true
		status := "<null>"
		if row["status"] != nil {
			status = row["status"].(string)
		}
		key := status + ":"
		if row["tag"] == nil {
			key += "<null>"
		} else {
			key += row["tag"].(string)
		}
		if row["ordinal"] == nil {
			ordinalBySourceAndTag[key] = math.NaN()
		} else {
			ordinalBySourceAndTag[key] = numericValue(row["ordinal"])
		}
	}
	if math.IsNaN(ordinalBySourceAndTag["active:red"]) || ordinalBySourceAndTag["active:red"] != 0 {
		t.Errorf("first red item ordinal = %v, want zero", ordinalBySourceAndTag["active:red"])
	}
	if ordinalBySourceAndTag["active:blue"] != 1 {
		t.Errorf("second note item ordinal = %v, want one", ordinalBySourceAndTag["active:blue"])
	}
	if !math.IsNaN(ordinalBySourceAndTag["inactive:<null>"]) {
		t.Errorf("empty-list preserve ordinal = %v, want null", ordinalBySourceAndTag["inactive:<null>"])
	}
	if got := executeConstructionOutput(t, ctx, client, constructionExpandOutput(), project, generation); !sameConstructionRowIdentities(expanded, got) {
		t.Fatalf("repeated expansion row identities = %#v, want %#v", constructionRowIdentities(got), constructionRowIdentities(expanded))
	}

	previewQuery := compileConstructionOutputQueryWithBindings(t, constructionExpandOutput(), project, generation, recipe.RuntimeBindings{
		IncludeSourceIdentity: true,
	})
	if containsString(previewQuery.PublicColumns, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("preview source ID entered public construction columns: %#v", previewQuery.PublicColumns)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, previewQuery)
	if len(previewRows) != len(expanded) {
		t.Fatalf("source-aware expansion returned %d rows, want %d", len(previewRows), len(expanded))
	}
	sourceCounts := map[string]int{}
	for _, row := range previewRows {
		sourceID, _ := row[ir.PreviewSourceResourceIDColumn].(string)
		if sourceID == "" {
			t.Errorf("expanded row has no starting FHIR ID: %#v", row)
			continue
		}
		sourceCounts[sourceID]++
	}
	wantSourceCounts := map[string]int{"tags-a": 2, "tags-b": 1, "tags-c": 1, "tags-d": 1}
	if !reflect.DeepEqual(sourceCounts, wantSourceCounts) {
		t.Fatalf("expanded row source IDs = %#v, want per-source counts %#v", sourceCounts, wantSourceCounts)
	}

	grouped := executeConstructionOutput(t, ctx, client, constructionExpandGroupOutput(), project, generation)
	want := map[string][]float64{
		"red":    {2, 2, 1},
		"blue":   {1, 1, 1},
		"<null>": {2, 1, 1},
	}
	got := make(map[string][]float64, len(grouped))
	for _, row := range grouped {
		key := "<null>"
		if row["tag"] != nil {
			key = row["tag"].(string)
		}
		got[key] = []float64{numericValue(row["rows"]), numericValue(row["status_count"]), numericValue(row["status_distinct"])}
		if identity, ok := row["__loom_row_id"].(string); !ok || identity == "" {
			t.Errorf("grouped row identity = %#v, want stable identity", row["__loom_row_id"])
		}
	}
	if len(got) != len(want) {
		t.Fatalf("grouped rows = %#v, want groups %#v", got, want)
	}
	for key, values := range want {
		actual, ok := got[key]
		if !ok || len(actual) != len(values) {
			t.Errorf("group %q = %#v, want %#v", key, actual, values)
			continue
		}
		for index := range values {
			if math.Abs(actual[index]-values[index]) > 1e-9 {
				t.Errorf("group %q metric %d = %v, want %v", key, index, actual[index], values[index])
			}
		}
	}
}

func TestConstructionGroupMissingKeyPoliciesAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, generation := "loom_construction_group_missing_policy_"+uuid.NewString(), "generation-group-missing-policy"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project == @project REMOVE document IN Observation",
			map[string]any{"project": project},
		); err != nil {
			t.Errorf("remove group policy fixtures: %v", err)
		}
	})
	insertConstructionReshapeRows(t, ctx, client, project, generation, []map[string]any{
		{"id": "missing-a"},
		{"id": "null-a", "status": nil},
		{"id": "active", "status": "active"},
		{"id": "inactive", "status": "inactive"},
	})

	grouped := executeConstructionOutput(t, ctx, client, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyGroup), project, generation)
	groupCounts := make(map[string]float64, len(grouped))
	for _, row := range grouped {
		key := "<missing>"
		if row["status"] != nil {
			key = row["status"].(string)
		}
		groupCounts[key] = numericValue(row["rows"])
	}
	if len(groupCounts) != 3 || groupCounts["<missing>"] != 2 || groupCounts["active"] != 1 || groupCounts["inactive"] != 1 {
		t.Fatalf("GROUP results = %#v, want missing=2, active=1, inactive=1", groupCounts)
	}

	excluded := executeConstructionOutput(t, ctx, client, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyExclude), project, generation)
	excludedCounts := make(map[string]float64, len(excluded))
	for _, row := range excluded {
		status, ok := row["status"].(string)
		if !ok {
			t.Fatalf("EXCLUDE retained a missing status group: %#v", row)
		}
		excludedCounts[status] = numericValue(row["rows"])
	}
	if len(excludedCounts) != 2 || excludedCounts["active"] != 1 || excludedCounts["inactive"] != 1 {
		t.Fatalf("EXCLUDE results = %#v, want only present status groups", excludedCounts)
	}

	errorOutput := constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyError)
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: errorOutput.Name, TranslationVersion: "test", Outputs: []recipe.Output{errorOutput}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build ERROR recipe plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve ERROR recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile ERROR recipe: %v", err)
	}
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile ERROR query: %v", err)
	}
	if err := client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil }); err == nil {
		t.Fatal("ERROR policy accepted absent or null group keys")
	}
}

func constructionMissingKeyPolicyOutput(policy recipe.ConstructionGroupMissingKeyPolicy) recipe.Output {
	return recipe.Output{
		Name: "construction_group_missing_key_policy", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status", Type: "string"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status", MissingKeyPolicy: policy,
					Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "group_status_id"}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "row_count_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "group_status_id", Name: "status", Type: "string"}, {ID: "row_count_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func constructionSummaryOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_summary_oracle", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "amount", ColumnID: "amount_id", Expr: recipe.Expression{Select: "root.valueQuantity.value"}},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "amount_id", Name: "amount"}},
			Steps: []recipe.ConstructionStep{{
				ID: "summary", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "whole_table_summary",
					Aggregates: []recipe.ConstructionGroupAggregate{
						{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
						{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
						{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
						{Operation: recipe.ConstructionGroupSum, InputColumnID: "amount_id", OutputColumnID: "amount_sum_id"},
						{Operation: recipe.ConstructionGroupMean, InputColumnID: "amount_id", OutputColumnID: "amount_mean_id"},
					},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "rows_id", Name: "rows"}, {ID: "status_count_id", Name: "status_count"},
					{ID: "status_distinct_id", Name: "status_distinct"}, {ID: "amount_sum_id", Name: "amount_sum"},
					{ID: "amount_mean_id", Name: "amount_mean"},
				},
			}},
		},
	}
}

func constructionCountRowsOnlyOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_group_count_rows_only", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}}},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}},
			Steps: []recipe.ConstructionStep{{
				ID: "count_rows", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "count_rows",
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				Outputs: []recipe.StageColumn{{ID: "rows_id", Name: "rows", Type: "integer"}},
			}},
		},
	}
}

func constructionGroupRowValuesOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_group_all_row_values", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_status", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_status",
					Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "status_id", OutputColumnID: "grouped_status_id"}},
					Aggregates:     []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"}},
				}},
				RowValues: []recipe.ConstructionRowValue{{InputColumnID: "tags_id", OutputColumnID: "tag_values_id", Policy: recipe.ConstructionRowValueAll}},
				Outputs: []recipe.StageColumn{
					{ID: "grouped_status_id", Name: "status", Type: "string"},
					{ID: "rows_id", Name: "rows", Type: "integer"},
					{ID: "tag_values_id", Name: "tag_values", Type: "array"},
				},
			}},
		},
	}
}

func constructionGroupOneRowValueOutput() recipe.Output {
	output := constructionGroupRowValuesOutput()
	output.Name = "construction_group_one_row_value"
	step := &output.Construction.Steps[0]
	step.RowValues[0].Policy = recipe.ConstructionRowValueOne
	step.Outputs[2].Name = "tag_value"
	step.Outputs[2].Type = "string"
	return output
}

func constructionExpandOutput() recipe.Output {
	return recipe.Output{
		Name: "construction_expand_oracle", RootResourceType: "Observation", RowGrain: "observation",
		Fields: []recipe.Field{
			{Name: "status", ColumnID: "status_id", Expr: recipe.Expression{Select: "root.status"}},
			{Name: "tags", ColumnID: "tags_id", Expr: recipe.Expression{Select: "root.note[].text"}, ValueMode: recipe.ValueModeAll},
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tags_id", Name: "tags"}},
			Steps: []recipe.ConstructionStep{{
				ID: "expand_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionExpandOp, Expand: &recipe.ConstructionExpand{
					ConstructionID: "expand_tag_values", InputColumnID: "tags_id", OutputColumnID: "tag_id",
					OrdinalColumnID: "ordinal_id", EmptyPolicy: recipe.ExpansionPreserveParent,
				}},
				Outputs: []recipe.StageColumn{{ID: "status_id", Name: "status"}, {ID: "tag_id", Name: "tag"}, {ID: "ordinal_id", Name: "ordinal"}},
			}},
		},
	}
}

func constructionExpandGroupOutput() recipe.Output {
	output := constructionExpandOutput()
	output.Name = "construction_expand_group_oracle"
	output.Construction.Steps = append(output.Construction.Steps, recipe.ConstructionStep{
		ID: "group_tags", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionStepOutputInput, StepID: "expand_tags"}},
		Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
			ConstructionID: "group_tag_values",
			Keys:           []recipe.ConstructionGroupKey{{InputColumnID: "tag_id", OutputColumnID: "grouped_tag_id"}},
			Aggregates: []recipe.ConstructionGroupAggregate{
				{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows_id"},
				{Operation: recipe.ConstructionGroupCountNonNull, InputColumnID: "status_id", OutputColumnID: "status_count_id"},
				{Operation: recipe.ConstructionGroupCountDistinct, InputColumnID: "status_id", OutputColumnID: "status_distinct_id"},
			},
		}},
		Outputs: []recipe.StageColumn{
			{ID: "grouped_tag_id", Name: "tag"}, {ID: "rows_id", Name: "rows"},
			{ID: "status_count_id", Name: "status_count"}, {ID: "status_distinct_id", Name: "status_distinct"},
		},
	})
	return output
}

func openConstructionReshapeArango(t *testing.T) (context.Context, *store.Client) {
	t.Helper()
	url, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if url == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must point to the supplied Docker Arango service")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	client, err := store.Open(ctx, url, database)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(context.Background()) })
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	return ctx, client
}

func insertConstructionReshapeRows(t *testing.T, ctx context.Context, client *store.Client, project, generation string, payloads []map[string]any) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(payloads))
	for _, payload := range payloads {
		id := payload["id"].(string)
		resource := map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		}
		if authResourcePath, ok := payload["auth_resource_path"]; ok {
			resource["auth_resource_path"] = authResourcePath
		}
		document, err := json.Marshal(resource)
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert construction reshape fixture: %v", err)
	}
}

func executeConstructionOutput(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string) []map[string]any {
	t.Helper()
	query := compileConstructionOutputQuery(t, output, project, generation)
	return executeReshapeOracleQuery(t, ctx, client, query)
}

func executeConstructionOutputError(t *testing.T, ctx context.Context, client *store.Client, output recipe.Output, project, generation string) error {
	t.Helper()
	query := compileConstructionOutputQuery(t, output, project, generation)
	return client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
}

func compileConstructionOutputQuery(t *testing.T, output recipe.Output, project, generation string) CompiledQuery {
	t.Helper()
	return compileConstructionOutputQueryWithBindings(t, output, project, generation, recipe.RuntimeBindings{})
}

func compileConstructionOutputQueryWithBindings(t *testing.T, output recipe.Output, project, generation string, bindings recipe.RuntimeBindings) CompiledQuery {
	t.Helper()
	bindings.Project, bindings.DatasetGeneration = project, generation
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	query, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile construction query: %v", err)
	}
	return query
}

func lowerConstructionOutput(t *testing.T, output recipe.Output, bindings recipe.RuntimeBindings) lower.CompiledRecipeOutput {
	t.Helper()
	project, generation := bindings.Project, bindings.DatasetGeneration
	bundle := recipe.Bundle{RecipeSchemaVersion: recipe.CurrentSchemaVersion, Name: output.Name, TranslationVersion: "test", Outputs: []recipe.Output{output}}
	plan, err := semantic.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build construction recipe plan: %v", err)
	}
	resolved, err := semantic.ResolveRecipePlan(plan, project, generation)
	if err != nil {
		t.Fatalf("resolve construction recipe plan: %v", err)
	}
	compiled, err := lower.CompileResolvedRecipePlan(resolved, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile construction recipe: %v", err)
	}
	return compiled.Outputs[0]
}

func TestPreviewSourceIdentityStaysPrivateThroughConstructionExpand(t *testing.T) {
	output := constructionExpandOutput()
	ordinary := compileConstructionOutputQuery(t, output, "source-id-project", "source-id-generation")
	preview := compileConstructionOutputQueryWithBindings(t, output, "source-id-project", "source-id-generation", recipe.RuntimeBindings{IncludeSourceIdentity: true})
	if strings.Contains(ordinary.Query, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("ordinary compilation gained a source ID projection:\n%s", ordinary.Query)
	}
	if !strings.Contains(preview.Query, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("preview compilation did not carry the source ID through EXPAND:\n%s", preview.Query)
	}
	if !strings.Contains(preview.Query, "root.id") {
		t.Fatalf("preview projection did not read the root FHIR id in scope:\n%s", preview.Query)
	}
	if containsString(preview.PublicColumns, ir.PreviewSourceResourceIDColumn) {
		t.Fatalf("private source ID entered public columns: %#v", preview.PublicColumns)
	}
	internalFound := false
	for _, column := range preview.OutputSchema {
		if column.Name == ir.PreviewSourceResourceIDColumn {
			internalFound = column.Internal
		}
	}
	if !internalFound {
		t.Fatalf("preview execution schema did not mark source ID as internal: %#v", preview.OutputSchema)
	}
	bindings := recipe.RuntimeBindings{Project: "source-id-project", DatasetGeneration: "source-id-generation", IncludeSourceIdentity: true}
	compiledOutput := lowerConstructionOutput(t, output, bindings)
	projectionNames := func(projections []ir.PhysicalProjection) []string {
		names := make([]string, 0, len(projections))
		for _, projection := range projections {
			names = append(names, projection.Name)
		}
		return names
	}
	stageColumnNames := func(columns []ir.PhysicalStageColumn) []string {
		names := make([]string, 0, len(columns))
		for _, column := range columns {
			names = append(names, column.Name)
		}
		return names
	}
	operationProjections := make([][]string, len(compiledOutput.Plan.Operations))
	for index, operation := range compiledOutput.Plan.Operations {
		if operation.Return != nil {
			operationProjections[index] = projectionNames(operation.Return.Projections)
		}
	}
	sequence := compiledOutput.Plan.StageSequence
	if sequence == nil {
		t.Fatal("construction EXPAND output has no physical stage sequence")
	}
	sourceColumns := stageColumnNames(sequence.SourceColumns)
	finalColumns := stageColumnNames(sequence.FinalColumns)
	stageInputs := make([][]string, len(sequence.Stages))
	stageOutputs := make([][]string, len(sequence.Stages))
	stageInputProjections := make([][]string, len(sequence.Stages))
	stageOutputProjections := make([][]string, len(sequence.Stages))
	for index, stage := range sequence.Stages {
		stageInputs[index] = stageColumnNames(stage.InputColumns)
		stageOutputs[index] = stageColumnNames(stage.OutputColumns)
		stageInputProjections[index] = projectionNames(stage.InputProjections)
		stageOutputProjections[index] = projectionNames(stage.OutputProjections)
	}
	if _, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 100, ir.DefaultPhysicalOptimizationPolicy()); err != nil {
		t.Fatalf("compile query-only clone with source identity: %v", err)
	}
	for index, operation := range compiledOutput.Plan.Operations {
		if operation.Return != nil && !reflect.DeepEqual(projectionNames(operation.Return.Projections), operationProjections[index]) {
			t.Fatalf("query-only compilation mutated frozen RETURN[%d] projections: before=%#v after=%#v", index, operationProjections[index], projectionNames(operation.Return.Projections))
		}
	}
	sequence = compiledOutput.Plan.StageSequence
	if !reflect.DeepEqual(stageColumnNames(sequence.SourceColumns), sourceColumns) || !reflect.DeepEqual(stageColumnNames(sequence.FinalColumns), finalColumns) {
		t.Fatalf("query-only compilation mutated frozen source/final stage columns: source=%#v/%#v final=%#v/%#v", sourceColumns, stageColumnNames(sequence.SourceColumns), finalColumns, stageColumnNames(sequence.FinalColumns))
	}
	for index, stage := range sequence.Stages {
		if !reflect.DeepEqual(stageColumnNames(stage.InputColumns), stageInputs[index]) || !reflect.DeepEqual(stageColumnNames(stage.OutputColumns), stageOutputs[index]) ||
			!reflect.DeepEqual(projectionNames(stage.InputProjections), stageInputProjections[index]) || !reflect.DeepEqual(projectionNames(stage.OutputProjections), stageOutputProjections[index]) {
			t.Fatalf("query-only compilation mutated frozen stage %d columns/projections", index)
		}
	}
}

func TestPreviewSourceIdentityNeverRegainsSingleAfterGroupOrPivot(t *testing.T) {
	basePlan := ir.PhysicalPlan{
		Engine: ir.PhysicalEngineAQL,
		Operations: []ir.PhysicalOperation{
			{Kind: ir.PhysicalRootScanOp, RootScan: &ir.PhysicalRootScan{Variable: "root"}},
			{Kind: ir.PhysicalReturnOp, Return: &ir.PhysicalReturn{}},
		},
	}
	tests := []struct {
		name   string
		output lower.CompiledRecipeOutput
	}{
		{
			name: "GROUPS",
			output: lower.CompiledRecipeOutput{Plan: ir.PhysicalPlan{
				Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalGroupRowsOp}},
			}},
		},
		{
			name: "grouped PIVOT",
			output: lower.CompiledRecipeOutput{Plan: ir.PhysicalPlan{
				Operations: []ir.PhysicalOperation{{Kind: ir.PhysicalGroupedPivotOp}},
			}},
		},
		{
			name: "group then Expand",
			output: lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{
				{Operation: string(recipe.ConstructionGroupOp)},
				{Operation: string(recipe.ConstructionExpandOp)},
			}},
		},
		{
			name: "pivot then Unpivot",
			output: lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{
				{Operation: string(recipe.ConstructionPivotOp)},
				{Operation: string(recipe.ConstructionUnpivotOp)},
			}},
		},
		{
			name: "CODED_GROUP",
			output: lower.CompiledRecipeOutput{Stages: []lower.CompiledStageDescriptor{
				{Operation: string(recipe.ConstructionCodedGroupOp)},
			}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, included, err := withPreviewSourceResourceID(test.output, basePlan)
			if err != nil {
				t.Fatal(err)
			}
			if included {
				t.Fatal("composite output regained a SINGLE source projection")
			}
		})
	}
}

func constructionRowIdentities(rows []map[string]any) []string {
	identities := make([]string, 0, len(rows))
	for _, row := range rows {
		identity, _ := row["__loom_row_id"].(string)
		identities = append(identities, identity)
	}
	return identities
}

func sameConstructionRowIdentities(left, right []map[string]any) bool {
	leftIDs, rightIDs := constructionRowIdentities(left), constructionRowIdentities(right)
	if len(leftIDs) != len(rightIDs) {
		return false
	}
	counts := make(map[string]int, len(leftIDs))
	for _, identity := range leftIDs {
		counts[identity]++
	}
	for _, identity := range rightIDs {
		counts[identity]--
		if counts[identity] < 0 {
			return false
		}
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func numericValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case int64:
		return float64(number)
	case int:
		return float64(number)
	default:
		return math.NaN()
	}
}
