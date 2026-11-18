package arango

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/authoringv2"
)

const tableShapeWorkspaceMigrationVersion = "wp17_tableshape_v1"

type workspaceMigrationTarget uint8

const (
	workspaceEncodingTarget workspaceMigrationTarget = iota
	tableShapeConstructionTarget
)

func (target workspaceMigrationTarget) version() string {
	if target == tableShapeConstructionTarget {
		return tableShapeWorkspaceMigrationVersion
	}
	return workspaceMigrationVersion
}

func (target workspaceMigrationTarget) projectionKey(source explorer.WorkspaceArtifact) string {
	if target == tableShapeConstructionTarget {
		return source.Key() + "_wp17"
	}
	return source.Key()
}

func (target workspaceMigrationTarget) migrate(raw []byte, catalog authoringv2.CatalogSnapshot) (authoringv2.StoredWorkspaceMigration, error) {
	if target == tableShapeConstructionTarget {
		return authoringv2.MigrateStoredTableShapeWorkspace(raw, catalog)
	}
	return authoringv2.MigrateStoredWorkspace(raw, catalog)
}

func (target workspaceMigrationTarget) validateProjection(projection explorer.WorkspaceProjection, source explorer.WorkspaceArtifact) error {
	if target == workspaceEncodingTarget {
		if projection.Source != source || projection.ID == "" || projection.ID != projection.ContentID() {
			return fmt.Errorf("workspace projection does not match its immutable source")
		}
		workspace, err := authoringv2.VerifyStoredWorkspaceForMigration(projection.Workspace, projection.Digest)
		if err != nil {
			return err
		}
		if workspace.SemanticsVersion != authoringv2.CurrentSemanticsVersion {
			return fmt.Errorf("workspace encoding projection is not current")
		}
		canonical, err := authoringv2.CanonicalStoredWorkspaceForMigration(workspace)
		if err != nil {
			return err
		}
		if fmt.Sprintf("sha256:%x", sha256.Sum256(canonical)) != projection.Digest {
			return fmt.Errorf("workspace encoding projection digest mismatch")
		}
		return nil
	}
	if _, err := projection.Validate(source); err != nil {
		return err
	}
	if target == tableShapeConstructionTarget {
		legacy, err := workspaceContainsTableShape(projection.Workspace)
		if err != nil {
			return err
		}
		if legacy {
			return fmt.Errorf("construction projection still contains tableShape")
		}
	}
	return nil
}

func workspaceContainsTableShape(raw []byte) (bool, error) {
	var workspace struct {
		Documents []map[string]json.RawMessage `json:"documents"`
	}
	if err := json.Unmarshal(raw, &workspace); err != nil {
		return false, err
	}
	for _, document := range workspace.Documents {
		if shape, exists := document["tableShape"]; exists && !bytes.Equal(bytes.TrimSpace(shape), []byte("null")) {
			return true, nil
		}
	}
	return false, nil
}
