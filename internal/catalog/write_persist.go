package catalog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bytedance/sonic"
)

func WriteFieldCatalog(ctx context.Context, client interface {
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}, collection string, docs []FieldCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
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

// WriteRelationshipCatalog persists committed edge cardinalities for a load.
func WriteRelationshipCatalog(ctx context.Context, client interface {
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}, docs []RelationshipCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
	if len(docs) == 0 {
		return nil
	}
	if batchSize <= 0 {
		batchSize = 1000
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
	if timings != nil {
		timings["relationship_catalog_marshal"] += time.Since(start).Seconds()
	}
	for i := 0; i < len(rawDocs); i += batchSize {
		end := i + batchSize
		if end > len(rawDocs) {
			end = len(rawDocs)
		}
		insertStart := time.Now()
		if err := client.InsertBatchRaw(ctx, RelationshipCatalogCollection, rawDocs[i:end], overwrite, writeAPI); err != nil {
			return err
		}
		if timings != nil {
			timings["relationship_catalog_insert"] += time.Since(insertStart).Seconds()
		}
	}
	return nil
}
