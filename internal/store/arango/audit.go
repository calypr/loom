package arango

import (
	driver "github.com/arangodb/go-driver/v2/arangodb"
	"slices"
	"strings"
)

const previewSupersedesMigrationVersion = "wp19_preview_supersedes_v1"

var previewQuantityCategoryLegacyFields = []string{"project", "dataset_generation", "auth_resource_path", "_key", "payload.id", "payload.status"}

var previewQuantityCategoryStoredValues = []string{"payload.valueQuantity"}

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

type PreviewIndexInventoryEntry struct {
	Collection   string   `json:"collection"`
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Fields       []string `json:"fields"`
	StoredValues []string `json:"storedValues"`
}

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
