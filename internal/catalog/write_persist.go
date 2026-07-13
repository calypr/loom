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

// WriteRelationshipCatalog persists the committed edge cardinalities for a
// load. The caller must invoke it only after all graph batches have succeeded.
func WriteRelationshipCatalog(ctx context.Context, client *arangostore.Client, docs []RelationshipCatalogDocument, batchSize int, overwrite bool, writeAPI string, timings map[string]float64) error {
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

// AccumulateRelationshipCatalog atomically adds committed edge counts to an
// existing legacy catalog. This is the append/import counterpart to
// WriteRelationshipCatalog; it avoids replacing counts from earlier resource
// files when the mutable loader runs with --truncate=false.
func AccumulateRelationshipCatalog(ctx context.Context, client *arangostore.Client, docs []RelationshipCatalogDocument, timings map[string]float64) error {
	if len(docs) == 0 {
		return nil
	}
	rows := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		rows = append(rows, map[string]any{
			"_key":               doc.Key,
			"project":            doc.Project,
			"dataset_generation": DatasetGenerationBindValue(doc.DatasetGeneration),
			"auth_resource_path": doc.AuthResourcePath,
			"from_type":          doc.FromType,
			"label":              doc.Label,
			"to_type":            doc.ToType,
			"edge_count":         doc.EdgeCount,
		})
	}
	start := time.Now()
	const query = `
FOR d IN @docs
  UPSERT { _key: d._key }
  INSERT d
  UPDATE { edge_count: OLD.edge_count + d.edge_count }
  IN fhir_relationship_catalog
  RETURN 1
`
	if err := client.QueryRows(ctx, query, len(rows), map[string]any{"docs": rows}, func(map[string]any) error { return nil }); err != nil {
		return err
	}
	if timings != nil {
		timings["relationship_catalog_accumulate"] += time.Since(start).Seconds()
	}
	return nil
}
