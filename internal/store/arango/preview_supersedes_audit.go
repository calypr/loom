package arango

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	driver "github.com/arangodb/go-driver/v2/arangodb"
)

const previewSupersedesMigrationVersion = "wp19_preview_supersedes_v1"
const previewSupersedesMigrationLedger = "loom_schema_migrations"

var previewQuantityCategoryLegacyFields = []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}
var previewQuantityCategoryStoredValues = []string{"payload.valueQuantity"}

// PreviewSupersededIndex is one exact compiler-owned legacy definition and its
// type-aware replacement. The definition is copied from Arango's inventory.
type PreviewSupersededIndex struct {
	Collection        string   `json:"collection"`
	OldID             string   `json:"oldID"`
	OldName           string   `json:"oldName"`
	OldFields         []string `json:"oldFields"`
	OldStoredValues   []string `json:"oldStoredValues"`
	NewName           string   `json:"newName"`
	NewFields         []string `json:"newFields"`
	NewStoredValues   []string `json:"newStoredValues"`
	ReplacementID     string   `json:"replacementID,omitempty"`
	ReplacementExists bool     `json:"replacementExists"`
	Kind              string   `json:"kind"`
}

// PreviewIndexInventoryEntry reports compiler-owned definitions that were not
// classified as a known superseded or current type-aware category index.
type PreviewIndexInventoryEntry struct {
	Collection   string   `json:"collection"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Fields       []string `json:"fields"`
	StoredValues []string `json:"storedValues"`
}

// PreviewSupersedesAudit is read-only. OtherOwnedIndexes includes compiler-
// named indexes outside the two replacement shapes; callers must preserve them.
type PreviewSupersedesAudit struct {
	SchemaVersion           int                          `json:"schemaVersion"`
	MigrationVersion        string                       `json:"migrationVersion"`
	Action                  string                       `json:"action"`
	CollectionsScanned      int                          `json:"collectionsScanned"`
	CompilerOwnedCount      int                          `json:"compilerOwnedCount"`
	LegacyCandidateCount    int                          `json:"legacyCandidateCount"`
	ReplacementPresentCount int                          `json:"replacementPresentCount"`
	OtherOwnedIndexes       []PreviewIndexInventoryEntry `json:"otherOwnedIndexes"`
	Candidates              []PreviewSupersededIndex     `json:"candidates"`
	LedgerPresent           bool                         `json:"ledgerPresent"`
	WouldRecordVersion      bool                         `json:"wouldRecordVersion"`
}

// AuditPreviewSupersededIndexes inventories exact supported legacy
// replacements without creating collections or indexes. It can run before
// any migration is applied and is safe against shared databases.
func (c *Client) AuditPreviewSupersededIndexes(ctx context.Context) (PreviewSupersedesAudit, error) {
	if c == nil || c.db == nil {
		return PreviewSupersedesAudit{}, errors.New("preview-index audit requires an open database")
	}
	audit := PreviewSupersedesAudit{
		SchemaVersion: 1, MigrationVersion: previewSupersedesMigrationVersion, Action: "audit",
		OtherOwnedIndexes: []PreviewIndexInventoryEntry{}, Candidates: []PreviewSupersededIndex{},
	}
	collections, err := c.db.Collections(ctx)
	if err != nil {
		return audit, fmt.Errorf("list collections for preview-index audit: %w", err)
	}
	for _, collection := range collections {
		name := collection.Name()
		if strings.HasPrefix(name, "_") || name == previewSupersedesMigrationLedger {
			continue
		}
		audit.CollectionsScanned++
		indexes, err := collection.Indexes(ctx)
		if err != nil {
			return audit, fmt.Errorf("list indexes in %q: %w", name, err)
		}
		entries, candidates, ownedCount := auditPreviewIndexCollection(name, indexes)
		audit.CompilerOwnedCount += ownedCount
		audit.OtherOwnedIndexes = append(audit.OtherOwnedIndexes, entries...)
		audit.Candidates = append(audit.Candidates, candidates...)
	}
	audit.LegacyCandidateCount = len(audit.Candidates)
	for _, candidate := range audit.Candidates {
		if candidate.ReplacementExists {
			audit.ReplacementPresentCount++
		}
	}
	sort.Slice(audit.Candidates, func(i, j int) bool {
		if audit.Candidates[i].Collection != audit.Candidates[j].Collection {
			return audit.Candidates[i].Collection < audit.Candidates[j].Collection
		}
		return audit.Candidates[i].OldName < audit.Candidates[j].OldName
	})
	sort.Slice(audit.OtherOwnedIndexes, func(i, j int) bool {
		if audit.OtherOwnedIndexes[i].Collection != audit.OtherOwnedIndexes[j].Collection {
			return audit.OtherOwnedIndexes[i].Collection < audit.OtherOwnedIndexes[j].Collection
		}
		return audit.OtherOwnedIndexes[i].Name < audit.OtherOwnedIndexes[j].Name
	})
	audit.LedgerPresent, err = c.previewSupersedesLedgerExists(ctx)
	if err != nil {
		return audit, err
	}
	return audit, nil
}

func auditPreviewIndexCollection(collection string, indexes []driver.IndexResponse) ([]PreviewIndexInventoryEntry, []PreviewSupersededIndex, int) {
	owned := make([]PreviewIndexInventoryEntry, 0)
	ownedCount := 0
	candidates := make([]PreviewSupersededIndex, 0)
	byName := make(map[string]driver.IndexResponse, len(indexes))
	for _, index := range indexes {
		byName[index.Name] = index
	}
	for _, index := range indexes {
		if !strings.HasPrefix(index.Name, previewCoveringIndexPrefix) {
			continue
		}
		ownedCount++
		fields, stored := previewIndexFields(index), previewIndexStoredValues(index)
		entry := PreviewIndexInventoryEntry{
			Collection: collection, ID: index.ID, Name: index.Name, Type: string(index.Type),
			Fields: append([]string(nil), fields...), StoredValues: append([]string(nil), stored...),
		}
		candidate, ok := previewSupersededCandidate(collection, index)
		if !ok {
			if !isPreviewSupersedesReplacement(collection, index) {
				owned = append(owned, entry)
			}
			continue
		}
		if current, exists := byName[candidate.NewName]; exists {
			if !previewCoveringIndexMatchesDefinition(current, candidate.NewFields, candidate.NewStoredValues) {
				// Keep the collision visible as an audit error via an impossible
				// marker; migration will fail closed in migratePreviewSupersededIndex.
				candidate.ReplacementExists = true
				candidate.ReplacementID = current.ID
				candidate.Kind += ":replacement-name-conflict"
			} else {
				candidate.ReplacementExists = true
				candidate.ReplacementID = current.ID
			}
		}
		candidates = append(candidates, candidate)
	}
	return owned, candidates, ownedCount
}

func previewSupersededCandidate(collection string, index driver.IndexResponse) (PreviewSupersededIndex, bool) {
	fields, stored := previewIndexFields(index), previewIndexStoredValues(index)
	if index.ID == "" || index.Type != driver.IndexType("persistent") || index.RegularIndex == nil ||
		index.Unique == nil || *index.Unique || index.Sparse == nil || *index.Sparse {
		return PreviewSupersededIndex{}, false
	}
	candidate := PreviewSupersededIndex{
		Collection: collection, OldID: index.ID, OldName: index.Name,
		OldFields: append([]string(nil), fields...), OldStoredValues: append([]string(nil), stored...),
	}
	switch {
	case len(fields) == 4 && len(stored) == 0 && fields[0] == "project" &&
		fields[1] == "dataset_generation" && strings.HasPrefix(fields[2], "payload.") && fields[3] == "auth_resource_path":
		if !validPreviewCoveringIndex(collection, index.Name, fields) || index.Name != previewCoveringIndexName(collection, fields) {
			return PreviewSupersededIndex{}, false
		}
		candidate.Kind = "category-field-only"
		candidate.NewFields = []string{"project", "dataset_generation", "resourceType", fields[2], "auth_resource_path"}
		candidate.NewStoredValues = []string{}
		candidate.NewName = previewCoveringIndexName(collection, candidate.NewFields)
	case collection == "Observation" && slices.Equal(fields, previewQuantityCategoryLegacyFields) &&
		slices.Equal(stored, previewQuantityCategoryStoredValues):
		if !validPreviewCoveringIndexWithStoredValues(collection, index.Name, fields, stored) ||
			index.Name != previewCoveringIndexNameWithStoredValues(collection, fields, stored) {
			return PreviewSupersededIndex{}, false
		}
		candidate.Kind = "observation-quantity-stored-values"
		candidate.NewFields = append(append([]string(nil), fields...), "resourceType")
		candidate.NewStoredValues = append([]string(nil), stored...)
		candidate.NewName = previewCoveringIndexNameWithStoredValues(collection, candidate.NewFields, stored)
	default:
		return PreviewSupersededIndex{}, false
	}
	if !validPreviewCoveringIndexWithStoredValuesIfNeeded(collection, candidate.NewName, candidate.NewFields, candidate.NewStoredValues) {
		return PreviewSupersededIndex{}, false
	}
	return candidate, true
}

func validPreviewCoveringIndexWithStoredValuesIfNeeded(collection, name string, fields, stored []string) bool {
	if len(stored) == 0 {
		return validPreviewCoveringIndex(collection, name, fields)
	}
	return validPreviewCoveringIndexWithStoredValues(collection, name, fields, stored)
}

func isPreviewSupersedesReplacement(collection string, index driver.IndexResponse) bool {
	fields, stored := previewIndexFields(index), previewIndexStoredValues(index)
	if index.Type != driver.IndexType("persistent") || index.RegularIndex == nil || index.Unique == nil || *index.Unique || index.Sparse == nil || *index.Sparse {
		return false
	}
	if len(fields) == 5 && fields[0] == "project" && fields[1] == "dataset_generation" && fields[2] == "resourceType" &&
		strings.HasPrefix(fields[3], "payload.") && fields[4] == "auth_resource_path" && len(stored) == 0 {
		return index.Name == previewCoveringIndexName(collection, fields)
	}
	if collection == "Observation" && slices.Equal(fields, append(append([]string(nil), previewQuantityCategoryLegacyFields...), "resourceType")) &&
		slices.Equal(stored, previewQuantityCategoryStoredValues) {
		return index.Name == previewCoveringIndexNameWithStoredValues(collection, fields, stored)
	}
	return false
}

func previewIndexFields(index driver.IndexResponse) []string {
	if index.RegularIndex == nil {
		return nil
	}
	return index.RegularIndex.Fields
}

func previewIndexStoredValues(index driver.IndexResponse) []string {
	if index.RegularIndex == nil {
		return nil
	}
	return index.RegularIndex.StoredValues
}

func (c *Client) previewSupersedesLedgerExists(ctx context.Context) (bool, error) {
	exists, err := c.db.CollectionExists(ctx, previewSupersedesMigrationLedger)
	if err != nil || !exists {
		return exists, err
	}
	found := false
	err = c.QueryRows(ctx, "FOR migration IN @@collection FILTER migration._key == @key LIMIT 1 RETURN {key: migration._key}", 1,
		map[string]interface{}{"@collection": previewSupersedesMigrationLedger, "key": previewSupersedesMigrationVersion},
		func(map[string]interface{}) error { found = true; return nil })
	return found, err
}
