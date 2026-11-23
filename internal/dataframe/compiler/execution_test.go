package compiler

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/model"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/calypr/loom/internal/dataframe/recipeplan"
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
	plan, err := recipeplan.BuildRecipePlan(bundle, bindings)
	if err != nil {
		t.Fatalf("build ERROR recipe plan: %v", err)
	}
	resolved, err := recipeplan.ResolveRecipePlan(plan, project, generation)
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

func TestSourceGroupPreviewMatchesGenericScopedExpandedGroupAgainstArango(t *testing.T) {
	project, generation := "loom_source_group_preview_"+uuid.NewString(), "generation_"+uuid.NewString()
	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation,
		AuthScopeMode: authscope.ReadScopeRestricted, AuthResourcePaths: []string{"/allowed"},
	}
	compiledOutput := lowerConstructionOutput(t, sourceGroupPreviewArangoOutput(), bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 25, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile key-paged source Group preview: %v", err)
	}
	full, err := CompileRecipeOutputWithPolicy(compiledOutput, bindings, 0, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile generic full-source Group: %v", err)
	}
	selectedKeys := strings.Index(preview.Query, "source_group_preview_selected_root_keys")
	canonicalGroup := strings.Index(preview.Query, "LET __loom_construction_stage_1 = (")
	hasFocusRelationship := false
	for _, value := range preview.BindVars {
		if relationship, ok := value.(string); ok && relationship == "focus_Specimen" {
			hasFocusRelationship = true
			break
		}
	}
	if selectedKeys < 0 || canonicalGroup < selectedKeys || !hasFocusRelationship ||
		!strings.Contains(preview.Query, "WITH COUNT INTO") || !strings.Contains(preview.Query, "__loom_physical_unnest_index_0 IN") {
		t.Fatalf("preview did not key-page before the unchanged routed COUNT_ROWS Group:\n%s", preview.Query)
	}

	ctx, client := openConstructionReshapeArango(t)
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{
		{Name: "Specimen"}, {Name: "Observation"}, {Name: "fhir_edge", Edge: true},
	}}); err != nil {
		t.Fatal(err)
	}
	key := func(id string) string { return project + "_" + id }
	owned := map[string][]string{"Specimen": {}, "Observation": {}, "fhir_edge": {}}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for collection, keys := range owned {
			if len(keys) == 0 {
				continue
			}
			query := fmt.Sprintf("FOR key IN @keys REMOVE key IN %s", collection)
			if err := client.ExecuteAQL(cleanupCtx, query, map[string]any{"keys": keys}); err != nil {
				t.Errorf("remove source Group preview fixtures from %s: %v", collection, err)
			}
		}
	})
	insert := func(collection string, documents []json.RawMessage) {
		t.Helper()
		if len(documents) == 0 {
			return
		}
		if err := client.InsertBatchRaw(ctx, collection, documents, false, "document"); err != nil {
			t.Fatalf("insert source Group preview fixtures into %s: %v", collection, err)
		}
	}
	resource := func(id, resourceType, docProject, docGeneration, authPath string, payload map[string]any) json.RawMessage {
		t.Helper()
		collection := resourceType
		owned[collection] = append(owned[collection], key(id))
		payload["id"], payload["resourceType"] = id, resourceType
		document, err := json.Marshal(map[string]any{
			"_key": key(id), "id": id, "project": docProject, "project_id": docProject,
			"dataset_generation": docGeneration, "resourceType": resourceType,
			"auth_resource_path": authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	edge := func(id, rootID, observationID, docProject, docGeneration, authPath string) json.RawMessage {
		t.Helper()
		owned["fhir_edge"] = append(owned["fhir_edge"], key(id))
		document, err := json.Marshal(map[string]any{
			"_key": key(id), "_from": "Observation/" + key(observationID), "_to": "Specimen/" + key(rootID),
			"project": docProject, "project_id": docProject, "dataset_generation": docGeneration,
			"auth_resource_path": authPath, "label": "focus_Specimen",
			"from_type": "Observation", "to_type": "Specimen",
		})
		if err != nil {
			t.Fatal(err)
		}
		return document
	}
	rootPayload := func(reference any, includeReference bool) map[string]any {
		subject := map[string]any{}
		if includeReference {
			subject["reference"] = reference
		}
		return map[string]any{"subject": subject}
	}
	coding := func(codes ...string) []any {
		items := make([]any, 0, len(codes))
		for _, code := range codes {
			items = append(items, map[string]any{"system": "urn:test", "code": code})
		}
		return items
	}
	observationPayload := func(codes ...string) map[string]any {
		return map[string]any{"valueCodeableConcept": map[string]any{"coding": coding(codes...)}}
	}
	roots := make([]json.RawMessage, 0, 45)
	observations := make([]json.RawMessage, 0, 45)
	edges := make([]json.RawMessage, 0, 48)
	addValid := func(rootID, observationID string, reference any, includeReference bool, codes ...string) {
		t.Helper()
		roots = append(roots, resource(rootID, "Specimen", project, generation, "/allowed", rootPayload(reference, includeReference)))
		observations = append(observations, resource(observationID, "Observation", project, generation, "/allowed", observationPayload(codes...)))
		edges = append(edges, edge("edge_"+rootID, rootID, observationID, project, generation, "/allowed"))
	}
	for index := 0; index < 27; index++ {
		rootID, observationID := fmt.Sprintf("group_%02d", index), fmt.Sprintf("observation_%02d", index)
		if index == 0 {
			addValid(rootID, observationID, rootID, true, "duplicate", "duplicate")
			edges = append(edges, edge("edge_group_00_duplicate", rootID, observationID, project, generation, "/allowed"))
			continue
		}
		addValid(rootID, observationID, rootID, true, "one")
	}
	addValid("missing_key", "observation_missing_key", nil, false, "one")
	addValid("null_key", "observation_null_key", nil, true, "one", "two")

	roots = append(roots, resource("malformed_ineligible", "Specimen", project, generation, "/allowed",
		rootPayload(map[string]any{"unexpected": "object"}, true)))
	observations = append(observations, resource("observation_malformed_ineligible", "Observation", project, generation, "/allowed", observationPayload()))
	roots = append(roots, resource("malformed_eligible", "Specimen", project, generation, "/allowed",
		rootPayload(map[string]any{"unexpected": "eligible-object"}, true)))

	roots = append(roots, resource("no_eligible_route", "Specimen", project, generation, "/allowed", rootPayload("00-no-route", true)))

	addInvalidRoute := func(rootID, observationID, relation string, edgeProject, edgeGeneration, edgeAuth, targetProject, targetGeneration, targetAuth string) {
		t.Helper()
		roots = append(roots, resource(rootID, "Specimen", project, generation, "/allowed", rootPayload("00-"+rootID, true)))
		observations = append(observations, resource(observationID, "Observation", targetProject, targetGeneration, targetAuth, observationPayload("one")))
		edges = append(edges, edge(relation, rootID, observationID, edgeProject, edgeGeneration, edgeAuth))
	}
	addInvalidRoute("edge_auth_denied", "target_for_edge_auth_denied", "edge_auth_denied", project, generation, "/blocked", project, generation, "/allowed")
	addInvalidRoute("edge_foreign_project", "target_for_edge_foreign_project", "edge_foreign_project", project+"_foreign", generation, "/allowed", project, generation, "/allowed")
	addInvalidRoute("edge_foreign_generation", "target_for_edge_foreign_generation", "edge_foreign_generation", project, generation+"_old", "/allowed", project, generation, "/allowed")
	addInvalidRoute("target_auth_denied", "target_auth_denied", "target_auth_denied", project, generation, "/allowed", project, generation, "/blocked")
	addInvalidRoute("target_foreign_project", "target_foreign_project", "target_foreign_project", project, generation, "/allowed", project+"_foreign", generation, "/allowed")
	addInvalidRoute("target_foreign_generation", "target_foreign_generation", "target_foreign_generation", project, generation, "/allowed", project, generation+"_old", "/allowed")
	insert("Specimen", roots)
	insert("Observation", observations)
	insert("fhir_edge", edges)

	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	fullRows := executeReshapeOracleQuery(t, ctx, client, full)
	if len(fullRows) != 28 || len(previewRows) != 25 || !reflect.DeepEqual(previewRows, fullRows[:25]) {
		t.Fatalf("key-paged first 25 source Groups differ from generic full-source order:\npreview %#v\nfull %#v", previewRows, fullRows)
	}
	if previewRows[0]["subject_reference"] != nil || previewRows[1]["subject_reference"] != "group_00" || previewRows[24]["subject_reference"] != "group_23" {
		t.Fatalf("first-page key ordering = %#v, want null followed by group_00 through group_23", previewRows)
	}
	if !constructionNumericEqual(previewRows[0]["rows"], 3) || !constructionNumericEqual(previewRows[1]["rows"], 4) {
		t.Fatalf("null/missing and duplicate-edge/coding multiplicities = %#v / %#v, want 3 / 4", previewRows[0], previewRows[1])
	}
	for _, row := range fullRows {
		if row["subject_reference"] == "00-no-route" || row["subject_reference"] == "00-edge_auth_denied" ||
			row["subject_reference"] == "00-edge_foreign_project" || row["subject_reference"] == "00-edge_foreign_generation" ||
			row["subject_reference"] == "00-target_auth_denied" || row["subject_reference"] == "00-target_foreign_project" ||
			row["subject_reference"] == "00-target_foreign_generation" {
			t.Errorf("ineligible scoped route entered canonical Group output: %#v", row)
		}
	}
	if !constructionNumericEqual(fullRows[len(fullRows)-1]["rows"], 1) || fullRows[len(fullRows)-1]["subject_reference"] != "group_26" {
		t.Fatalf("last generic Group = %#v, want group_26 after the 25-row preview boundary", fullRows[len(fullRows)-1])
	}

	var orderedTypes []string
	orderQuery := `FOR root IN Specimen
FILTER root.project == @project AND root.dataset_generation == @dataset_generation
  AND root.auth_resource_path IN @auth_resource_paths
COLLECT group_key = root.payload.subject.reference
SORT group_key ASC
RETURN {kind: TYPENAME(group_key)}`
	if err := client.QueryRows(ctx, orderQuery, 100, map[string]any{
		"project": project, "dataset_generation": generation, "auth_resource_paths": []string{"/allowed"},
	}, func(row map[string]any) error {
		orderedTypes = append(orderedTypes, fmt.Sprint(row["kind"]))
		return nil
	}); err != nil {
		t.Fatalf("verify malformed-key order beyond preview page: %v", err)
	}
	objectPosition := -1
	for index, kind := range orderedTypes {
		if kind == "object" {
			objectPosition = index
			break
		}
	}
	if objectPosition < 25 {
		t.Fatalf("malformed object-key group position = %d in Arango sort order (%v), want beyond preview limit 25", objectPosition, orderedTypes)
	}

	eligibleTarget := resource("observation_malformed_eligible", "Observation", project, generation, "/allowed", observationPayload())
	insert("Observation", []json.RawMessage{eligibleTarget})
	eligibleEdge := edge("edge_malformed_eligible", "malformed_eligible", "observation_malformed_eligible", project, generation, "/allowed")
	insert("fhir_edge", []json.RawMessage{eligibleEdge})
	previewErr := executeSourceGroupPreviewError(ctx, client, preview)
	if previewErr == nil || !strings.Contains(previewErr.Error(), "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH") {
		t.Fatalf("eligible malformed STRING key preview error = %v, want CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH despite being beyond the first 25 groups", previewErr)
	}
	fullErr := executeSourceGroupPreviewError(ctx, client, full)
	if fullErr == nil || !strings.Contains(fullErr.Error(), "CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH") {
		t.Fatalf("generic Group error for eligible malformed key = %v, want CONSTRUCTION_GROUP_KEY_TYPE_MISMATCH", fullErr)
	}
}

func sourceGroupPreviewArangoOutput() recipe.Output {
	return recipe.Output{
		Name: "source_group_preview_arango", RootResourceType: "Specimen", RootOccurrenceID: "specimen-root", RowGrain: "expanded",
		Fields: []recipe.Field{{Name: "subject_reference", ColumnID: "subject-reference", Expr: recipe.Expression{Select: "root.subject.reference"}}},
		Traversals: []recipe.Traversal{{
			Name: "focus_Specimen", OccurrenceID: "observation-owner", Alias: "observation", ToResourceType: "Observation",
		}},
		Expand: &recipe.Expansion{
			OwnerOccurrenceID: "observation-owner",
			From:              recipe.Expression{Select: "observation.valueCodeableConcept.coding[]"},
			As:                "coding", EmptyPolicy: recipe.ExpansionPreserveParent,
		},
		Construction: &recipe.Construction{
			Version:       1,
			SourceColumns: []recipe.StageColumn{{ID: "subject-reference", Name: "subject_reference", Type: "string", Nullable: true}},
			Steps: []recipe.ConstructionStep{{
				ID: "group_subject_reference", Inputs: []recipe.ConstructionInputRef{{Kind: recipe.ConstructionSourceProjectionInput}},
				Operation: recipe.ConstructionOperation{Kind: recipe.ConstructionGroupOp, Group: &recipe.ConstructionGroup{
					ConstructionID: "group_subject_reference", MissingKeyPolicy: recipe.ConstructionGroupMissingKeyGroup,
					Keys:       []recipe.ConstructionGroupKey{{InputColumnID: "subject-reference", OutputColumnID: "subject-reference-group"}},
					Aggregates: []recipe.ConstructionGroupAggregate{{Operation: recipe.ConstructionGroupCountRows, OutputColumnID: "rows"}},
				}},
				Outputs: []recipe.StageColumn{
					{ID: "subject-reference-group", Name: "subject_reference", Type: "string", Nullable: true},
					{ID: "rows", Name: "rows", Type: "integer"},
				},
			}},
		},
	}
}

func executeSourceGroupPreviewError(ctx context.Context, client *store.Client, query model.CompiledQuery) error {
	return client.QueryRows(ctx, query.Query, 500, query.BindVars, func(map[string]any) error { return nil })
}
