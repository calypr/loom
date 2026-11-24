package compilerfixtures

import (
	"context"
	"encoding/json"
	"testing"

	store "github.com/calypr/loom/internal/store/arango"
)

func InsertReshapeAgreementFixture(t *testing.T, ctx context.Context, client *store.Client, project, generation string) {
	t.Helper()
	documents := make([]json.RawMessage, 0, len(ReshapeOracleSourceRows))
	for _, row := range ReshapeOracleSourceRows {
		payload := map[string]any{
			"id": row.SourceID, "resourceType": "Observation", "status": *row.Group.Text.String,
			"valueInteger": *row.NumericCategory.Integer, "valueString": row.Text,
			"code":         map[string]any{"text": *row.StringCategory.String},
			"valueBoolean": row.Flag, "issued": row.Unrelated,
		}
		if row.Number == nil {
			payload["valueQuantity"] = map[string]any{"value": nil}
		} else {
			payload["valueQuantity"] = map[string]any{"value": *row.Number}
		}
		encoded, err := json.Marshal(map[string]any{
			"_key": project + "_" + row.SourceID, "id": row.SourceID, "project": project, "project_id": project,
			"dataset_generation": generation, "resourceType": "Observation", "auth_resource_path": "/allowed", "payload": payload,
		})
		if err != nil {
			t.Fatal(err)
		}
		documents = append(documents, encoded)
	}
	if err := client.InsertBatchRaw(ctx, "Observation", documents, false, "document"); err != nil {
		t.Fatalf("insert lineage agreement fixture: %v", err)
	}
}
