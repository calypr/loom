package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/calypr/loom/internal/catalog"
	catalogarango "github.com/calypr/loom/internal/catalog/arango"
)

func (p *RepositorySourceLoader) copyGenerationForward(ctx context.Context, record sourceLoadRecord) error {
	identity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return err
	}
	for _, resourceType := range identity.GeneratedResourceTypes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if sourceLoadSelectsType(record.Plan.Files, resourceType) {
			continue
		}
		if err := p.copySourceCollection(ctx, resourceType, record.SourceGeneration, record.Project, record.Generation, copyVertexDocument(resourceType, record.Project, record.Generation)); err != nil {
			return fmt.Errorf("copy source generation resource %s: %w", resourceType, err)
		}
	}
	selectedTypes := make([]string, 0, len(record.Plan.Files))
	for _, file := range record.Plan.Files {
		resourceType, err := canonicalSourceResourceType(file.Path)
		if err != nil {
			return err
		}
		selectedTypes = append(selectedTypes, resourceType)
	}
	if err := p.copySourceCollection(ctx, EdgeCollection, record.SourceGeneration, record.Project, record.Generation, copyEdgeDocument(record.Project, record.Generation, record.SourceGeneration, selectedTypes)); err != nil {
		return fmt.Errorf("copy source generation edges: %w", err)
	}
	if err := p.copySourceCollection(ctx, catalog.FieldCatalogCollection, record.SourceGeneration, record.Project, record.Generation, copyCatalogDocument(catalog.FieldCatalogCollection, record.Project, record.Generation, selectedTypes)); err != nil {
		return fmt.Errorf("copy source generation field catalog: %w", err)
	}
	if err := p.copySourceCollection(ctx, catalog.SemanticInventoryCollection, record.SourceGeneration, record.Project, record.Generation, copySemanticInventoryDocument(record.Project, record.Generation, selectedTypes)); err != nil {
		return fmt.Errorf("copy source generation semantic inventory: %w", err)
	}
	return nil
}

type sourceCopyRow struct {
	Document       map[string]any `json:"document"`
	FromLogicalKey string         `json:"from_logical_key,omitempty"`
	ToLogicalKey   string         `json:"to_logical_key,omitempty"`
}

type sourceCopyTransform func(sourceCopyRow) (json.RawMessage, error)

func (p *RepositorySourceLoader) copySourceCollection(ctx context.Context, collection, sourceGeneration, project, targetGeneration string, transform sourceCopyTransform) error {
	exists, err := p.client.CollectionExists(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	query := `FOR doc IN @@collection
FILTER doc.project == @project
FILTER (@source_generation == "" AND NOT_NULL(doc.dataset_generation, "") == "") OR doc.dataset_generation == @source_generation
FILTER doc._key > @after_key
SORT doc._key
LIMIT @limit
RETURN {document: doc}`
	if collection == EdgeCollection {
		query = `FOR doc IN @@collection
FILTER doc.project == @project
FILTER (@source_generation == "" AND NOT_NULL(doc.dataset_generation, "") == "") OR doc.dataset_generation == @source_generation
FILTER doc._key > @after_key
SORT doc._key
LIMIT @limit
LET from_doc = DOCUMENT(doc._from)
LET to_doc = DOCUMENT(doc._to)
RETURN {
  document: doc,
  from_logical_key: NOT_NULL(from_doc.logical_key, from_doc._key, ""),
  to_logical_key: NOT_NULL(to_doc.logical_key, to_doc._key, "")
}`
	}
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows := make([]sourceCopyRow, 0, p.batchSize)
		err := p.client.QueryRows(ctx, query, p.batchSize, map[string]any{
			"@collection": collection, "project": project, "source_generation": sourceGeneration,
			"after_key": after, "limit": p.batchSize,
		}, func(row map[string]any) error {
			var copyRow sourceCopyRow
			encoded, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if err := json.Unmarshal(encoded, &copyRow); err != nil {
				return err
			}
			if copyRow.Document == nil {
				return fmt.Errorf("source copy returned an empty %s document", collection)
			}
			rows = append(rows, copyRow)
			return nil
		})
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		docs := make([]json.RawMessage, 0, len(rows))
		for _, row := range rows {
			sourceKey, _ := row.Document["_key"].(string)
			if sourceKey == "" {
				return fmt.Errorf("source copy returned a %s document without _key", collection)
			}
			encoded, err := transform(row)
			if err != nil {
				return fmt.Errorf("rebase %s document: %w", collection, err)
			}
			if len(encoded) > 0 {
				docs = append(docs, encoded)
			}
			after = sourceKey
		}
		if len(docs) > 0 {
			if err := p.client.InsertBatchRaw(ctx, collection, docs, true, p.writeAPI); err != nil {
				return fmt.Errorf("write candidate %s copy batch: %w", collection, err)
			}
		}
		if len(rows) < p.batchSize {
			return nil
		}
	}
}

func copyVertexDocument(resourceType, project, generation string) sourceCopyTransform {
	return func(row sourceCopyRow) (json.RawMessage, error) {
		doc := row.Document
		if sourceLoadString(doc["project"]) != project {
			return nil, fmt.Errorf("vertex project does not match source-load project")
		}
		logicalKey := sourceLoadString(doc[logicalKeyField])
		if logicalKey == "" {
			logicalKey = sourceLoadString(doc["_key"])
		}
		if logicalKey == "" {
			return nil, fmt.Errorf("vertex has no stable logical key")
		}
		delete(doc, "_id")
		delete(doc, "_rev")
		doc[logicalKeyField] = logicalKey
		doc[generationIdentityField] = generation
		doc["source_file"] = resourceType
		doc["_key"] = generationDocumentKey(project, generation, resourceType, logicalKey)
		return json.Marshal(doc)
	}
}

func copyEdgeDocument(project, generation, sourceGeneration string, selectedTypes []string) sourceCopyTransform {
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
		logicalKey := sourceLoadString(doc[logicalKeyField])
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
		namespaced, err := namespaceEdgeDocument(jsonRawDocument(doc), project, generation)
		if err != nil {
			return nil, err
		}
		rebased, err := decodeTopLevelDocument(namespaced)
		if err != nil {
			return nil, err
		}
		rebased["source_file"] = json.RawMessage(strconvQuote(owner))
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
		doc[generationIdentityField] = generation
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
		buildID := catalog.SemanticInventoryBuildID(project, generation)
		ordinal := numericInt64(doc["ordinal"])
		authPath := sourceLoadString(doc["auth_resource_path"])
		sourceKind := sourceLoadString(doc["source_kind"])
		doc["dataset_generation"] = generation
		doc["build_id"] = buildID
		doc["_key"] = semanticInventoryContributionKey(project, generation, authPath, resourceType, buildID, sourceID, int(ordinal), sourceKind)
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

func (p *RepositorySourceLoader) validateSourceLoadSelectedScopes(ctx context.Context, project string, base sourceLoadBase, selectedTypes []string, authResourcePath string) error {
	for _, resourceType := range selectedTypes {
		if priorScope, ok := base.LegacyFileScopes[resourceType]; ok && priorScope != authResourcePath {
			return &SourcePublishMigrationRequiredError{Reason: "selected file is owned by a different authorization path"}
		}
	}
	collections := append([]string(nil), selectedTypes...)
	collections = append(collections, catalog.FieldCatalogCollection, catalog.SemanticInventoryCollection)
	for _, collection := range collections {
		if exists, err := p.client.CollectionExists(ctx, collection); err != nil {
			return err
		} else if !exists {
			continue
		}
		query := `FOR doc IN @@collection
FILTER doc.project == @project
FILTER (@generation == "" AND NOT_NULL(doc.dataset_generation, "") == "") OR doc.dataset_generation == @generation
FILTER @scope_field == "resource_type" ? doc.resource_type IN @resource_types : doc.resourceType == @resource_type
FILTER NOT_NULL(doc.auth_resource_path, "") != @auth_resource_path
LIMIT 1
RETURN {mismatch: true}`
		fieldName := "resourceType"
		if collection == catalog.FieldCatalogCollection || collection == catalog.SemanticInventoryCollection {
			fieldName = "resource_type"
		}
		found, err := p.sourceLoadQueryHasRow(ctx, query, map[string]any{
			"@collection": collection, "project": project, "generation": base.Generation,
			"scope_field": fieldName, "resource_type": collection,
			"resource_types": selectedTypes, "auth_resource_path": authResourcePath,
		})
		if err != nil {
			return fmt.Errorf("inspect selected %s authorization scope: %w", collection, err)
		}
		if found {
			return &SourcePublishMigrationRequiredError{Reason: "selected resource rows use a different authorization path"}
		}
	}
	if exists, err := p.client.CollectionExists(ctx, EdgeCollection); err != nil {
		return err
	} else if exists {
		found, err := p.sourceLoadQueryHasRow(ctx, `FOR edge IN fhir_edge
FILTER edge.project == @project
FILTER (@generation == "" AND NOT_NULL(edge.dataset_generation, "") == "") OR edge.dataset_generation == @generation
FILTER NOT_NULL(edge.source_file, "") IN @resource_types
FILTER NOT_NULL(edge.auth_resource_path, "") != @auth_resource_path
LIMIT 1
RETURN {mismatch: true}`, map[string]any{
			"project": project, "generation": base.Generation, "resource_types": selectedTypes,
			"auth_resource_path": authResourcePath,
		})
		if err != nil {
			return fmt.Errorf("inspect selected edge authorization scope: %w", err)
		}
		if found {
			return &SourcePublishMigrationRequiredError{Reason: "selected relationship rows use a different authorization path"}
		}
	}
	return nil
}

func (p *RepositorySourceLoader) requireMutableSourceCopySafe(ctx context.Context, project string, base sourceLoadBase, selectedTypes []string) error {
	if base.Mode != "mutable" {
		return nil
	}
	identity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return err
	}
	allSelected := len(selectedTypes) == len(identity.GeneratedResourceTypes)
	if allSelected {
		for _, resourceType := range identity.GeneratedResourceTypes {
			if !sourceLoadSelectsTypeNames(selectedTypes, resourceType) {
				allSelected = false
				break
			}
		}
	}
	if allSelected {
		return nil
	}
	for _, resourceType := range identity.GeneratedResourceTypes {
		if sourceLoadSelectsTypeNames(selectedTypes, resourceType) {
			continue
		}
		if exists, err := p.client.CollectionExists(ctx, resourceType); err != nil {
			return err
		} else if !exists {
			continue
		}
		found, err := p.sourceLoadQueryHasRow(ctx, `FOR doc IN @@collection
FILTER doc.project == @project AND NOT_NULL(doc.dataset_generation, "") == ""
LIMIT 1 RETURN {found: true}`, map[string]any{"@collection": resourceType, "project": project})
		if err != nil {
			return fmt.Errorf("inspect mutable-root %s rows: %w", resourceType, err)
		}
		if found {
			return &SourcePublishMigrationRequiredError{Reason: "partial update cannot copy unversioned mutable-root rows safely"}
		}
	}
	for _, collection := range []string{catalog.FieldCatalogCollection, catalog.SemanticInventoryCollection} {
		if exists, err := p.client.CollectionExists(ctx, collection); err != nil {
			return err
		} else if !exists {
			continue
		}
		found, err := p.sourceLoadQueryHasRow(ctx, `FOR doc IN @@collection
FILTER doc.project == @project AND NOT_NULL(doc.dataset_generation, "") == ""
FILTER doc.resource_type NOT IN @resource_types
LIMIT 1 RETURN {found: true}`, map[string]any{"@collection": collection, "project": project, "resource_types": selectedTypes})
		if err != nil {
			return fmt.Errorf("inspect mutable-root %s rows: %w", collection, err)
		}
		if found {
			return &SourcePublishMigrationRequiredError{Reason: "partial update cannot copy unversioned mutable-root catalogs safely"}
		}
	}
	if exists, err := p.client.CollectionExists(ctx, EdgeCollection); err != nil {
		return err
	} else if exists {
		found, err := p.sourceLoadQueryHasRow(ctx, `FOR edge IN fhir_edge
FILTER edge.project == @project AND NOT_NULL(edge.dataset_generation, "") == ""
FILTER (NOT_NULL(edge.source_file, "") == "" OR edge.source_file NOT IN @resource_types)
LIMIT 1 RETURN {found: true}`, map[string]any{"project": project, "resource_types": selectedTypes})
		if err != nil {
			return fmt.Errorf("inspect mutable-root edge ownership: %w", err)
		}
		if found {
			return &SourcePublishMigrationRequiredError{Reason: "partial update cannot copy mutable-root edges without file ownership"}
		}
	}
	return nil
}

func (p *RepositorySourceLoader) sourceLoadQueryHasRow(ctx context.Context, query string, binds map[string]any) (bool, error) {
	found := false
	err := p.client.QueryRows(ctx, query, 1, binds, func(map[string]any) error {
		found = true
		return nil
	})
	return found, err
}

func sourceLoadString(value any) string {
	text, _ := value.(string)
	return text
}

func semanticInventoryContributionKey(project, generation, authPath, resourceType, buildID, sourceID string, ordinal int, sourceKind string) string {
	parts := []string{"semantic-inventory-contribution/v1", project, catalog.NormalizeDatasetGeneration(generation), authPath, resourceType, buildID, sourceID, strconv.Itoa(ordinal)}
	if sourceKind == catalog.SemanticInventorySourceRetained {
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

func (p *RepositorySourceLoader) prepareCandidateSemanticInventory(ctx context.Context, record sourceLoadRecord) error {
	store, err := catalogarango.New(p.client)
	if err != nil {
		return err
	}
	build := catalog.NewSemanticInventoryBuild(record.Project, record.Generation, record.Plan.AuthResourcePath)
	return store.BeginSemanticInventoryBuild(ctx, build)
}

func (p *RepositorySourceLoader) finalizeSourceLoadCandidate(ctx context.Context, record sourceLoadRecord) (int64, error) {
	identity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return 0, err
	}
	selectedTypes := make([]string, 0, len(record.Plan.Files))
	for _, file := range record.Plan.Files {
		resourceType, err := canonicalSourceResourceType(file.Path)
		if err != nil {
			return 0, err
		}
		selectedTypes = append(selectedTypes, resourceType)
	}
	if err := p.rebuildCandidateRelationshipCatalog(ctx, record); err != nil {
		return 0, err
	}
	store, err := catalogarango.New(p.client)
	if err != nil {
		return 0, err
	}
	summary, err := store.RebuildResourceInventory(ctx, catalog.ResourceInventoryRebuildOptions{
		Project: record.Project, DatasetGeneration: record.Generation,
		ResourceTypes: identity.GeneratedResourceTypes, CursorBatch: p.batchSize,
		BatchSize: p.batchSize, WriteAPI: p.writeAPI,
	})
	if err != nil {
		return 0, fmt.Errorf("rebuild candidate resource inventory: %w", err)
	}
	return summary.DocumentCount, nil
}

func (p *RepositorySourceLoader) rebuildCandidateRelationshipCatalog(ctx context.Context, record sourceLoadRecord) error {
	query := `FOR edge IN fhir_edge
  FILTER edge.project == @project AND edge.dataset_generation == @generation
  FILTER edge.from_type IN @resource_types AND edge.to_type IN @resource_types
  COLLECT project = edge.project, dataset_generation = edge.dataset_generation,
    auth_resource_path = NOT_NULL(edge.auth_resource_path, ""),
    from_type = edge.from_type, label = edge.label, to_type = edge.to_type
    WITH COUNT INTO edge_count
  SORT project, dataset_generation, auth_resource_path, from_type, label, to_type
  RETURN {project, dataset_generation, auth_resource_path, from_type, label, to_type, edge_count}`
	identity, err := loadSchemaSnapshot(p.schemaPath)
	if err != nil {
		return err
	}
	batch := make([]json.RawMessage, 0, p.batchSize)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := p.client.InsertBatchRaw(ctx, catalog.RelationshipCatalogCollection, batch, true, p.writeAPI)
		batch = batch[:0]
		return err
	}
	if err := p.client.QueryRows(ctx, query, p.batchSize, map[string]any{
		"project": record.Project, "generation": record.Generation,
		"resource_types": identity.GeneratedResourceTypes,
	}, func(row map[string]any) error {
		key := catalog.RelationshipKey{
			Project: sourceLoadString(row["project"]), DatasetGeneration: sourceLoadString(row["dataset_generation"]),
			AuthResourcePath: sourceLoadString(row["auth_resource_path"]), FromType: sourceLoadString(row["from_type"]),
			Label: sourceLoadString(row["label"]), ToType: sourceLoadString(row["to_type"]),
		}
		count := sourceLoadNumber(row["edge_count"])
		docs := catalog.RelationshipCatalogDocuments(map[catalog.RelationshipKey]int64{key: count})
		if len(docs) != 1 {
			return fmt.Errorf("relationship aggregate has no stable catalog identity")
		}
		encoded, err := json.Marshal(docs[0])
		if err != nil {
			return err
		}
		batch = append(batch, encoded)
		if len(batch) >= p.batchSize {
			return flush()
		}
		return nil
	}); err != nil {
		return fmt.Errorf("aggregate candidate relationship catalog: %w", err)
	}
	if err := flush(); err != nil {
		return fmt.Errorf("write candidate relationship catalog: %w", err)
	}
	return nil
}
