package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/calypr/loom/internal/dataframe/compiler/ir"
	store "github.com/calypr/loom/internal/store/arango"
	"github.com/google/uuid"
)

func TestCurrentPivotNullMissingExclusionPartitionAgainstArango(t *testing.T) {
	endpoint, database := os.Getenv("LOOM_TEST_ARANGO_URL"), os.Getenv("LOOM_TEST_ARANGO_DATABASE")
	if endpoint == "" || database == "" {
		t.Skip("LOOM_TEST_ARANGO_URL and LOOM_TEST_ARANGO_DATABASE must name the supplied local Arango fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client, err := store.OpenExisting(ctx, endpoint, database)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close(context.Background())
	if err := client.Bootstrap(ctx, store.BootstrapSpec{Collections: []store.CollectionSpec{{Name: "Observation"}}}); err != nil {
		t.Fatal(err)
	}
	project := "loom_null_missing_exclusion_" + uuid.NewString()
	const generation = "generation-current-null-missing-exclusion"

	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := client.QueryRows(cleanupCtx, `FOR d IN Observation FILTER d.project == @project AND d.dataset_generation == @generation REMOVE d IN Observation RETURN {_key: OLD._key}`, 10, map[string]any{"project": project, "generation": generation}, func(map[string]any) error { return nil }); err != nil {
			t.Errorf("remove owned exclusion fixture: %v", err)
		}
	}()
	documents := make([]json.RawMessage, 0, 3)
	for _, row := range []struct {
		id    string
		code  map[string]any
		value int
	}{
		{id: "null", code: map[string]any{"text": nil}, value: 10},
		{id: "missing", code: map[string]any{}, value: 20},
		{id: "other", code: map[string]any{"text": "other"}, value: 30},
	} {
		payload := map[string]any{"resourceType": "Observation", "id": row.id, "status": "final", "valueInteger": 1, "code": row.code, "valueQuantity": map[string]any{"value": row.value}}
		raw, err := json.Marshal(map[string]any{"_key": project + "_" + row.id, "id": row.id, "project": project, "project_id": project, "dataset_generation": generation, "resourceType": "Observation", "payload": payload})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, raw)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatal(err)
	}
	compiled, preview, err := compileCurrentConstructionOracle(currentNullMissingExclusionOutput(), project, generation, 10)
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	previewRows := executeReshapeOracleQuery(t, ctx, client, preview)
	if len(previewRows) != 1 {
		t.Fatalf("preview rows = %#v, want one final group", previewRows)
	}
	row := previewRows[0]
	for name, expected := range map[string]any{"group_text": "final", "group_number": float64(1), "null_value": float64(10), "missing_value": float64(20), "__loom_reshape_unlisted_count": float64(1)} {
		if row[name] != expected {
			t.Fatalf("preview %s = %#v, want %#v", name, row[name], expected)
		}
	}
	const rowID = `["GROUPED_PIVOT","s04-pivot-sum",["STRING","final"],["INTEGER",1]]`
	if row["__loom_row_id"] != rowID {
		t.Fatalf("preview identity = %#v, want %s", row["__loom_row_id"], rowID)
	}
	query, err := CompileTableShapeExclusionsWithPolicy(compiled, 0, 10, ir.DefaultPhysicalOptimizationPolicy())
	if err != nil {
		t.Fatal(err)
	}
	exclusions := []map[string]any{}
	if err := client.QueryRows(ctx, query.Query, 10, query.BindVars, func(value map[string]any) error { exclusions = append(exclusions, value); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(exclusions) != 1 {
		t.Fatalf("exclusions = %#v, want only the unlisted other source", exclusions)
	}
	excluded := exclusions[0]
	if excluded[query.ResourceTypeField] != "Observation" || excluded[query.ResourceIDField] != "other" {
		t.Fatalf("excluded identity = %#v, want Observation/other", excluded)
	}
	assertExclusionLiteral(t, excluded, "other", true, "STRING", rowID)
	after, err := json.Marshal(compiled.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("preview/exclusion compilation mutated the source plan")
	}
}
