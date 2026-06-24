package catalog

import (
	"context"
	"encoding/json"
	"time"

	arangostore "github.com/calypr/loom/internal/store/arango"

	"github.com/bytedance/sonic"
)

func WriteFieldCatalog(ctx context.Context, client *arangostore.Client, collection string, docs []FieldCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	if len(docs) == 0 {
		return nil
	}

	start := time.Now()
	rawDocs := make([]json.RawMessage, 0, len(docs))
	for _, doc := range docs {
		data, err := sonic.ConfigFastest.Marshal(&doc)
		if err != nil {
			return err
		}
		rawDocs = append(rawDocs, json.RawMessage(data))
	}
	timings["field_catalog_marshal"] += time.Since(start).Seconds()

	for i := 0; i < len(rawDocs); i += batchSize {
		end := i + batchSize
		if end > len(rawDocs) {
			end = len(rawDocs)
		}
		insertStart := time.Now()
		if err := client.InsertBatchRaw(ctx, collection, rawDocs[i:end], overwrite, writeAPI); err != nil {
			return err
		}
		timings["field_catalog_insert"] += time.Since(insertStart).Seconds()
	}
	return nil
}
