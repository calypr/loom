package compiler

import (
	"context"
	"encoding/json"
	"math"
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
	for _, bufferedRows := range []string{"construction_group_input_rows", "construction_group_all_rows"} {
		if strings.Contains(query.Query, bufferedRows) {
			t.Errorf("count-only query retains %q:\n%s", bufferedRows, query.Query)
		}
	}

	keyedQuery := compileConstructionOutputQuery(t, constructionMissingKeyPolicyOutput(recipe.ConstructionGroupMissingKeyError), "construction-count-rows-only", "generation-count-rows-only")
	if !strings.Contains(keyedQuery.Query, "WITH COUNT INTO ") || !strings.Contains(keyedQuery.Query, "CONSTRUCTION_GROUP_MISSING_KEY") {
		t.Fatalf("keyed count query did not retain its missing-key error behavior:\n%s", keyedQuery.Query)
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
		document, err := json.Marshal(map[string]any{
			"_key": project + "_" + id, "id": id, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "payload": payload,
		})
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

func compileConstructionOutputQuery(t *testing.T, output recipe.Output, project, generation string) CompiledQuery {
	t.Helper()
	bindings := recipe.RuntimeBindings{Project: project, DatasetGeneration: generation}
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
	query, err := CompileRecipeOutputWithPolicy(compiled.Outputs[0], bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatalf("compile construction query: %v", err)
	}
	return query
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
