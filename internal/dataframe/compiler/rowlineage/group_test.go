package rowlineage_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	"github.com/calypr/loom/internal/dataframe/compiler/rowlineage"
	"github.com/calypr/loom/internal/dataframe/recipe"
	"github.com/google/uuid"
)

func TestConstructionCodedGroupRowLineagePagesScopedContributorsAgainstArango(t *testing.T) {
	ctx, client := openConstructionReshapeArango(t)
	project, foreignProject, generation := "loom_coded_lineage_"+uuid.NewString(), "loom_coded_lineage_foreign_"+uuid.NewString(), "generation-coded-lineage"
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document.project IN @projects REMOVE document IN Observation",
			map[string]any{"projects": []string{project, foreignProject}},
		); err != nil {
			t.Errorf("remove CODED_GROUP row-lineage fixtures: %v", err)
		}
	})

	type fixture struct {
		project, generation, id, authPath, coding string
	}
	fixtures := []fixture{
		{project, generation, "coded-a", "Observation/visible", "duplicate"},
		{project, generation, "coded-b", "Observation/visible", "shared"},
		{project, generation, "coded-c", "Observation/visible", "shared"},
		{project, generation, "coded-denied", "Observation/hidden", "shared"},
		{project, "old-generation", "coded-old", "Observation/visible", "shared"},
		{foreignProject, generation, "coded-foreign", "Observation/visible", "shared"},
		{project, generation, "missing-a", "Observation/visible", "incomplete"},
		{project, generation, "missing-b", "Observation/visible", "absent"},
		{project, generation, "missing-denied", "Observation/hidden", "absent"},
		{project, "old-generation", "missing-old", "Observation/visible", "incomplete"},
		{foreignProject, generation, "missing-foreign", "Observation/visible", "absent"},
	}
	documents := make([]json.RawMessage, 0, len(fixtures))
	for _, item := range fixtures {
		payload := map[string]any{"id": item.id, "resourceType": "Observation"}
		switch item.coding {
		case "duplicate":
			payload["component"] = []any{codedGroupComponent("shared"), codedGroupComponent("shared")}
		case "shared":
			payload["component"] = []any{codedGroupComponent("shared")}
		case "incomplete":
			payload["component"] = []any{map[string]any{"code": map[string]any{"coding": []any{
				map[string]any{"system": "urn:loom:inline-test"},
			}}}}
		case "absent":
		default:
			t.Fatalf("unknown test coding kind %q", item.coding)
		}
		document, err := json.Marshal(map[string]any{
			"_key": item.project + "_" + item.id, "id": item.id,
			"project": item.project, "project_id": item.project, "dataset_generation": item.generation,
			"resourceType": "Observation", "auth_resource_path": item.authPath, "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, document)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert CODED_GROUP row-lineage fixtures: %v", err)
	}

	bindings := recipe.RuntimeBindings{
		Project: project, DatasetGeneration: generation, AuthScopeMode: authscope.ReadScopeRestricted,
		AuthResourcePaths: []string{"Observation/visible"},
	}
	output := constructionCodedGroupTestOutput()
	compiled := lowerConstructionOutput(t, output, bindings)
	preview, err := CompileRecipeOutputWithPolicy(compiled, bindings, 100, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, preview)
	var codedRow, missingRow map[string]any
	for _, row := range rows {
		switch row["code"] {
		case "shared":
			codedRow = row
		case nil:
			missingRow = row
		}
	}
	if len(rows) != 2 || codedRow == nil || missingRow == nil || !constructionNumericEqual(codedRow["source_records"], 3) ||
		!constructionNumericEqual(missingRow["source_records"], 2) {
		t.Fatalf("scoped coded rows = %#v, want three shared-code roots and two missing-code roots", rows)
	}
	getRowID := func(row map[string]any) string {
		t.Helper()
		rowID, ok := row["__loom_row_id"].(string)
		if !ok || rowID == "" {
			t.Fatalf("CODED_GROUP row identity = %#v", row["__loom_row_id"])
		}
		return rowID
	}
	compilePage := func(rowID string, offset int) rowlineage.CompiledRowLineageQuery {
		t.Helper()
		query, compileErr := rowlineage.CompileRowLineageOutput(compiled, rowID, offset, 1, ir.DefaultPhysicalOptimizationPolicy())
		if compileErr != nil {
			t.Fatalf("compile CODED_GROUP row-lineage page at %d: %v", offset, compileErr)
		}
		for _, want := range []string{
			"root.project == @project", "root.dataset_generation == @dataset_generation",
			"root.auth_resource_path IN @auth_resource_paths",
		} {
			if !strings.Contains(query.Query, want) {
				t.Errorf("CODED_GROUP row-lineage query lost source scope %q:\n%s", want, query.Query)
			}
		}
		return query
	}
	assertContributor := func(got any, id string) {
		t.Helper()
		contributor, _ := got.(map[string]any)
		if contributor["resourceType"] != "Observation" || contributor["resourceId"] != id || contributor["occurrenceKey"] != project+"_"+id {
			t.Errorf("CODED_GROUP contributor = %#v, want Observation/%s", contributor, id)
		}
	}
	for offset, id := range []string{"coded-a", "coded-b", "coded-c"} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(getRowID(codedRow), offset))
		contributors, _ := result["contributors"].([]any)
		wantMore := offset < 2
		if result["found"] != true || result["hasMore"] != wantMore || len(contributors) != 1 {
			t.Fatalf("CODED_GROUP page at %d = %#v, want one exact contributor and hasMore=%t", offset, result, wantMore)
		}
		assertContributor(contributors[0], id)
	}
	missingRowID := getRowID(missingRow)
	missing := executeRowLineageOracleQuery(t, ctx, client, compilePage(missingRowID, 0))
	missingContributors, _ := missing["contributors"].([]any)
	if missing["found"] != true || missing["hasMore"] != true || len(missingContributors) != 1 {
		t.Fatalf("valid missing-code row lineage = %#v, want its first contributor and another page", missing)
	}
	assertContributor(missingContributors[0], "missing-a")
	missingSecond := executeRowLineageOracleQuery(t, ctx, client, compilePage(missingRowID, 1))
	missingSecondContributors, _ := missingSecond["contributors"].([]any)
	if missingSecond["found"] != true || missingSecond["hasMore"] != false || len(missingSecondContributors) != 1 {
		t.Fatalf("second missing-code row lineage page = %#v, want final contributor", missingSecond)
	}
	assertContributor(missingSecondContributors[0], "missing-b")

	var identity []any
	if err := json.Unmarshal([]byte(missingRowID), &identity); err != nil || len(identity) < 2 {
		t.Fatalf("decode CODED_GROUP row identity %q: %v", missingRowID, err)
	}
	identity[1] = "different_group"
	wrongStageBytes, err := json.Marshal(identity)
	if err != nil {
		t.Fatal(err)
	}
	for _, forgedID := range []string{"forged-coded-row-id", string(wrongStageBytes)} {
		result := executeRowLineageOracleQuery(t, ctx, client, compilePage(forgedID, 0))
		if result["found"] != false || result["hasMore"] != false || len(result["contributors"].([]any)) != 0 {
			t.Errorf("forged/wrong-stage CODED_GROUP identity disclosed contributors: %#v", result)
		}
	}
}
