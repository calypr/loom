package workspace

import "encoding/json"

func migrateStoredDocumentFixture(document Document) (Document, error) {
	if document.ArchiveRetiredShape == nil {
		return StartDocumentConstruction(document)
	}
	raw, err := json.Marshal(constructionWorkspace(document))
	if err != nil {
		return Document{}, err
	}
	result, err := MigrateStoredTableShapeWorkspace(raw, CatalogSnapshot{})
	if err != nil {
		return Document{}, err
	}
	return result.Workspace.Documents[0], nil
}
func cloneStoredShapeFixture(shape *storedTableShape) (*storedTableShape, error) {
	raw, err := json.Marshal(shape)
	if err != nil {
		return nil, err
	}
	var out *storedTableShape
	err = json.Unmarshal(raw, &out)
	return out, err
}
func storedDigestFixture(workspace Workspace) (string, error) {
	raw, err := CanonicalStoredWorkspaceForMigration(workspace)
	if err != nil {
		return "", err
	}
	return workspaceDigest(raw), nil
}
