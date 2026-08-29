package catalog

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/bytedance/sonic"
	fhirschema "github.com/calypr/loom/internal/fhir/schema"
)

const relationshipCatalogKeyPrefix = "rfc_"

// rawRelationshipEdge is the small subset of an edge document needed to
// build the ingest-owned relationship catalog. It deliberately does not
// depend on a generated FHIR type: graph edge routing is schema-owned and the
// persisted edge contract is stable across generated and generic loaders.
type rawRelationshipEdge struct {
	Project           string `json:"project"`
	DatasetGeneration string `json:"dataset_generation"`
	AuthResourcePath  string `json:"auth_resource_path"`
	FromType          string `json:"from_type"`
	Label             string `json:"label"`
	ToType            string `json:"to_type"`
}

// RelationshipCountsFromRawEdges counts only complete, routable edge
// documents. Callers add these counts to their committed-write result only
// after InsertBatchRaw succeeds, so failed batches can never advertise facts
// that are absent from Arango.
func RelationshipCountsFromRawEdges(docs []json.RawMessage) (map[RelationshipKey]int64, error) {
	counts := make(map[RelationshipKey]int64)
	for index, raw := range docs {
		var edge rawRelationshipEdge
		if err := sonic.Unmarshal(raw, &edge); err != nil {
			return nil, fmt.Errorf("decode relationship edge %d: %w", index, err)
		}
		if edge.Project == "" || edge.FromType == "" || edge.Label == "" || edge.ToType == "" {
			return nil, fmt.Errorf("relationship edge %d is missing project/from_type/label/to_type", index)
		}
		fromType, ok := fhirschema.ConcreteResourceType(edge.FromType)
		if !ok {
			return nil, fmt.Errorf("relationship edge %d has unsupported from_type %q", index, edge.FromType)
		}
		toType, ok := fhirschema.ConcreteResourceType(edge.ToType)
		if !ok {
			return nil, fmt.Errorf("relationship edge %d has unsupported to_type %q", index, edge.ToType)
		}
		key := RelationshipKey{
			Project:           edge.Project,
			DatasetGeneration: NormalizeDatasetGeneration(edge.DatasetGeneration),
			AuthResourcePath:  edge.AuthResourcePath,
			FromType:          fromType,
			Label:             edge.Label,
			ToType:            toType,
		}
		counts[key]++
	}
	return counts, nil
}

func MergeRelationshipCounts(dst, src map[RelationshipKey]int64) {
	for key, count := range src {
		dst[key] += count
	}
}

func RelationshipCatalogDocuments(counts map[RelationshipKey]int64) []RelationshipCatalogDocument {
	keys := make([]RelationshipKey, 0, len(counts))
	for key, count := range counts {
		if count > 0 {
			keys = append(keys, key)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		left, right := keys[i], keys[j]
		for _, pair := range [][2]string{{left.Project, right.Project}, {left.DatasetGeneration, right.DatasetGeneration}, {left.AuthResourcePath, right.AuthResourcePath}, {left.FromType, right.FromType}, {left.Label, right.Label}, {left.ToType, right.ToType}} {
			if pair[0] != pair[1] {
				return pair[0] < pair[1]
			}
		}
		return false
	})
	docs := make([]RelationshipCatalogDocument, 0, len(keys))
	for _, key := range keys {
		docs = append(docs, RelationshipCatalogDocument{
			Key:               relationshipCatalogKey(key),
			Project:           key.Project,
			DatasetGeneration: NormalizeDatasetGeneration(key.DatasetGeneration),
			AuthResourcePath:  key.AuthResourcePath,
			FromType:          key.FromType,
			Label:             key.Label,
			ToType:            key.ToType,
			EdgeCount:         counts[key],
		})
	}
	return docs
}

func relationshipCatalogKey(key RelationshipKey) string {
	return relationshipCatalogKeyPrefix + catalogIdentityDigest(
		"relationship-catalog/v1",
		key.Project,
		NormalizeDatasetGeneration(key.DatasetGeneration),
		key.AuthResourcePath,
		key.FromType,
		key.Label,
		key.ToType,
	)
}
