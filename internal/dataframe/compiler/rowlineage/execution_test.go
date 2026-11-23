package rowlineage_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/lower"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

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

	compilePage := func(rowID string, offset int) rowlineage.CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := rowlineage.CompileRowLineageOutput(output, rowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
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

	compilePage := func(compiled lower.CompiledRecipeOutput, id string, offset int) rowlineage.CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := rowlineage.CompileRowLineageOutput(compiled, id, offset, 50, ir.DefaultPhysicalOptimizationPolicy())
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
