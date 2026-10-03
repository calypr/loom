package compiler

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/recipe"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestGroupedPivotReservedPhysicalOutputNameAgainstArango(t *testing.T) {
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
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := client.Close(closeCtx); err != nil {
			t.Errorf("close reserved-name pivot fixture client: %v", err)
		}
	})
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}

	project := "loom_reserved_pivot_name_" + uuid.NewString()
	generation := "generation_" + uuid.NewString()
	documentKey := project + "_observation_1"
	t.Logf("fixture project: %s", project)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.ExecuteAQL(cleanupCtx,
			"FOR document IN Observation FILTER document._key == @key REMOVE document IN Observation",
			map[string]any{"key": documentKey},
		); err != nil {
			t.Errorf("remove owned reserved-name pivot fixture: %v", err)
		}
	})
	document, err := json.Marshal(map[string]any{
		"_key": documentKey, "id": "observation-1", "project": project, "project_id": project,
		"dataset_generation": generation, "resourceType": "Observation",
		"payload": map[string]any{
			"id": "observation-1", "resourceType": "Observation", "status": "final",
			"code": map[string]any{"text": "null"}, "valueQuantity": map[string]any{"value": 7},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", []json.RawMessage{document}, false, "document"); err != nil {
		t.Fatalf("insert reserved-name pivot fixture: %v", err)
	}

	output := reshapeOracleOutput("reserved_physical_output", &recipe.TableReshape{
		Kind: recipe.TableReshapeGroupedPivot,
		GroupedPivot: &recipe.GroupedPivot{
			ConstructionID: "reserved_physical_output_pivot", GroupKeys: []string{"group_text"},
			CategoryColumn: "string_category", ValueColumn: "numeric_value",
			Categories:      []recipe.GroupedPivotCategory{{Key: reshapeOracleString("null"), Output: "null", Label: "Null"}},
			DuplicatePolicy: recipe.PivotDuplicateError, MissingCellPolicy: recipe.PivotMissingCellNull,
			UnlistedCategoryPolicy: recipe.PivotUnlistedCategoryError,
		},
	})
	_, query, err := compileReshapeOracle(output, project, generation, 100)
	if err != nil {
		t.Fatalf("compile reserved-name grouped pivot: %v", err)
	}
	rows := executeReshapeOracleQuery(t, ctx, client, query)
	if len(rows) != 1 {
		t.Fatalf("reserved-name pivot returned %d rows, want one: %#v", len(rows), rows)
	}
	if got := rows[0]["null"]; got != float64(7) {
		t.Fatalf("reserved physical output %q = %#v, want 7", "null", got)
	}
}
