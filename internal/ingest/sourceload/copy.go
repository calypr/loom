package sourceload

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/calypr/loom/internal/catalog"
	publication "github.com/calypr/loom/internal/dataset"
	"github.com/calypr/loom/internal/ingest/identity"
	"github.com/calypr/loom/internal/semantic"
	"slices"
	"strconv"
	"strings"
)

func sourceSchemaAllowsCopy(base, current publication.SchemaSnapshot, selectedTypes []string) (bool, error) {
	if base.SchemaSHA256 == current.SchemaSHA256 {
		return true, nil
	}
	for _, resourceType := range current.GeneratedResourceTypes {
		if !slices.Contains(selectedTypes, resourceType) {
			return false, &SourcePublishMigrationRequiredError{Reason: "FHIR schema changed and a full reload is required"}
		}
	}
	return false, nil
}

type sourceCopyRow struct {
	Document       map[string]any `json:"document"`
	FromLogicalKey string         `json:"from_logical_key,omitempty"`
	ToLogicalKey   string         `json:"to_logical_key,omitempty"`
}

type sourceCopyTransform func(sourceCopyRow) (json.RawMessage, error)

func copyVertexDocument(resourceType, project, generation, identityFormat string) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		if sourceLoadString(doc["project"]) != project {
			return nil, fmt.Errorf("vertex project does not match source-load project")
		}
		logicalKey := sourceLoadString(doc[identity.LogicalKeyField])
		if logicalKey == "" {
			logicalKey = sourceLoadString(doc["_key"])
		}
		if logicalKey == "" {
			return nil, fmt.Errorf("vertex has no stable logical key")
		}
		delete(doc, "_id")
		delete(doc, "_rev")
		doc[identity.LogicalKeyField] = logicalKey
		doc[identity.GenerationIdentityField] = generation
		doc["source_file"] = resourceType
		doc["_key"] = identity.GenerationDocumentKeyWithFormat(project, generation, resourceType, logicalKey, identityFormat)
		return json.Marshal(doc)
	}
}

func copyEdgeDocument(project, generation, sourceGeneration string, selectedTypes []string, identityFormat string) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		if sourceLoadString(doc["project"]) != project {
			return nil, fmt.Errorf("edge project does not match source-load project")
		}
		physicalFrom, fromPhysicalOK := doc["_from"].(string)
		physicalTo, toPhysicalOK := doc["_to"].(string)
		if !fromPhysicalOK || !toPhysicalOK {
			return nil, fmt.Errorf("edge is missing physical endpoints")
		}
		fromType, _, fromPhysicalPath := strings.Cut(physicalFrom, "/")
		toType, _, toPhysicalPath := strings.Cut(physicalTo, "/")
		if !fromPhysicalPath || !toPhysicalPath {
			return nil, fmt.Errorf("edge endpoint is not collection/key")
		}
		if sourceLoadSelectsTypeNames(selectedTypes, fromType) && sourceLoadSelectsTypeNames(selectedTypes, toType) {
			return nil, nil
		}
		from, fromOK := doc["logical_from"].(string)
		to, toOK := doc["logical_to"].(string)
		if !fromOK || from == "" || !toOK || to == "" {
			from, fromOK = doc["_from"].(string)
			to, toOK = doc["_to"].(string)
			if sourceGeneration != "" {
				fromCollection, _, fromPathOK := strings.Cut(from, "/")
				toCollection, _, toPathOK := strings.Cut(to, "/")
				if !fromPathOK || !toPathOK || row.FromLogicalKey == "" || row.ToLogicalKey == "" {
					return nil, &SourcePublishMigrationRequiredError{Reason: "a legacy edge has no logical endpoint metadata and an endpoint row is missing"}
				}
				from = fromCollection + "/" + row.FromLogicalKey
				to = toCollection + "/" + row.ToLogicalKey
			}
		}
		fromCollection, fromLogicalKey, fromPathOK := strings.Cut(from, "/")
		toCollection, toLogicalKey, toPathOK := strings.Cut(to, "/")
		if !fromPathOK || !toPathOK || fromCollection == "" || toCollection == "" || fromLogicalKey == "" || toLogicalKey == "" {
			return nil, fmt.Errorf("edge endpoint is not collection/key")
		}
		owner := sourceLoadString(doc["source_file"])
		if owner == "" {
			storedFromType, storedToType := sourceLoadString(doc["from_type"]), sourceLoadString(doc["to_type"])
			if storedFromType != storedToType {
				return nil, &SourcePublishMigrationRequiredError{Reason: "legacy forward/reverse edge ownership is not recorded"}
			}
			owner = storedFromType
		}
		if sourceLoadSelectsTypeNames(selectedTypes, owner) {
			return nil, nil
		}
		logicalKey := sourceLoadString(doc[identity.LogicalKeyField])
		if logicalKey == "" {
			logicalKey = sourceLoadString(doc["_key"])
		}
		if logicalKey == "" {
			return nil, fmt.Errorf("edge has no stable logical key")
		}
		doc["_key"] = logicalKey
		doc["_from"] = from
		doc["_to"] = to
		delete(doc, "_id")
		delete(doc, "_rev")
		namespaced, err := identity.NamespaceEdgeDocumentWithFormat(jsonRawDocument(doc), project, generation, identityFormat)
		if err != nil {
			return nil, err
		}
		rebased, err := identity.DecodeTopLevelDocument(namespaced)
		if err != nil {
			return nil, err
		}
		rebased["source_file"] = json.RawMessage(identity.QuoteDocumentString(owner))
		encoded, err := json.Marshal(rebased)
		return encoded, err
	}
}

func copyCatalogDocument(collection, project, generation string, selectedTypes []string) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		if sourceLoadSelectsTypeNames(selectedTypes, sourceLoadString(doc["resource_type"])) {
			return nil, nil
		}
		delete(doc, "_id")
		delete(doc, "_rev")
		oldKey := sourceLoadString(doc["_key"])
		if oldKey == "" {
			return nil, fmt.Errorf("catalog record has no key")
		}
		doc["_key"] = sourceLoadCopiedCatalogKey(collection, project, generation, oldKey)
		doc[identity.GenerationIdentityField] = generation
		return json.Marshal(doc)
	}
}

func copySemanticInventoryDocument(project, generation string, selectedTypes []string) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		delete(doc, "_id")
		delete(doc, "_rev")
		if sourceLoadString(doc["project"]) != project {
			return nil, fmt.Errorf("semantic inventory project does not match source-load project")
		}
		resourceType := sourceLoadString(doc["resource_type"])
		if sourceLoadSelectsTypeNames(selectedTypes, resourceType) {
			return nil, nil
		}
		sourceID := sourceLoadString(doc["source_id"])
		if resourceType == "" || sourceID == "" {
			return nil, fmt.Errorf("semantic inventory contribution is missing its source identity")
		}
		buildID := semantic.BuildID(project, generation)
		ordinal := numericInt64(doc["ordinal"])
		authPath := sourceLoadString(doc["auth_resource_path"])
		sourceKind := sourceLoadString(doc["source_kind"])
		doc["dataset_generation"] = generation
		doc["build_id"] = buildID
		doc["_key"] = semanticInventoryContributionKey(project, generation, authPath, resourceType, buildID, sourceID, int(ordinal), sourceKind)
		return json.Marshal(doc)
	}
}

func copySemanticInventoryEntryDocument(project, generation string, selectedTypes []string, copyCurrentFileEntries bool) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		delete(doc, "_id")
		delete(doc, "_rev")
		if sourceLoadString(doc["project"]) != project {
			return nil, fmt.Errorf("semantic inventory entry project does not match source-load project")
		}
		resourceType := sourceLoadString(doc["resource_type"])
		if sourceLoadSelectsTypeNames(selectedTypes, resourceType) {
			return nil, nil
		}
		bindingID := sourceLoadString(doc["binding_id"])
		conceptID := sourceLoadString(doc["concept_id"])
		sourceKind := sourceLoadString(doc["source_kind"])
		if resourceType == "" || bindingID == "" || conceptID == "" || sourceKind == "" {
			return nil, fmt.Errorf("semantic inventory entry is missing its stable identity")
		}
		if sourceKind == semantic.SourceFile && !copyCurrentFileEntries {
			return nil, nil
		}
		buildID := semantic.BuildID(project, generation)
		doc["dataset_generation"] = generation
		doc["build_id"] = buildID
		doc["_key"] = semantic.EntryKey(project, generation, buildID, sourceKind,
			sourceLoadString(doc["auth_resource_path"]), bindingID, conceptID)
		return json.Marshal(doc)
	}
}

func sourceLoadCopiedCatalogKey(collection, project, generation, oldKey string) string {
	digest := sha256.Sum256([]byte("loom-source-load-copied-catalog-v1\x00" + collection + "\x00" + project + "\x00" + generation + "\x00" + oldKey))
	return "slc_" + hex.EncodeToString(digest[:])
}

func sourceLoadSelectsType(files []SourceManifestFile, resourceType string) bool {
	for _, file := range files {
		if selected, err := canonicalSourceResourceType(file.Path); err == nil && selected == resourceType {
			return true
		}
	}
	return false
}

func sourceLoadSelectsTypeNames(types []string, resourceType string) bool {
	for _, selected := range types {
		if selected == resourceType {
			return true
		}
	}
	return false
}

func sourceLoadString(value any) string {
	text, _ := value.(string)
	return text
}

func semanticInventoryContributionKey(project, generation, authPath, resourceType, buildID, sourceID string, ordinal int, sourceKind string) string {
	parts := []string{"semantic-inventory-contribution/v1", project, catalog.NormalizeDatasetGeneration(generation), authPath, resourceType, buildID, sourceID, strconv.Itoa(ordinal)}
	if sourceKind == semantic.SourceRetained {
		parts = append(parts, sourceKind)
	}
	hash := sha256.New()
	for _, part := range parts {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(part))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func jsonRawDocument(document map[string]any) json.RawMessage {
	encoded, _ := json.Marshal(document)
	return encoded
}
