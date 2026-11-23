package arango

import (
	"context"
	"fmt"
	"github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/explorer"
	store "github.com/calypr/loom/internal/store/arango"
)

const workspaceProjectionCollection = "loom_explorer_workspace_projections"
const workspaceMigrationVersion = "wp18_workspace_v1"
const workspaceMigrationLedger = "loom_schema_migrations"

func workspaceProjectionCollectionSpec() store.CollectionSpec {
	return store.CollectionSpec{Name: workspaceProjectionCollection}
}

func (s *Store) WorkspaceProjection(ctx context.Context, source explorer.WorkspaceArtifact) (explorer.WorkspaceProjection, error) {
	var projection *explorer.WorkspaceProjection
	err := s.client.QueryRows(ctx, `LET migrations = (
 FOR migration IN @@ledger FILTER migration._key IN [@encodingVersion,@constructionVersion] RETURN migration
)
LET valid = LENGTH(migrations[* FILTER CURRENT.version != CURRENT._key]) == 0
LET versions = migrations[*]._key
LET constructionComplete = POSITION(versions,@constructionVersion)
FOR projection IN @@projections
 FILTER valid AND ((projection._key == @constructionKey AND constructionComplete) OR
  (projection._key == @encodingKey AND POSITION(versions,@encodingVersion)))
 SORT projection._key == @constructionKey ? 0 : 1
 LIMIT 1 RETURN {projection:JSON_STRINGIFY(projection), constructionComplete:constructionComplete}`, 1,
		map[string]any{"@ledger": workspaceMigrationLedger, "encodingVersion": workspaceMigrationVersion,
			"constructionVersion": tableShapeWorkspaceMigrationVersion, "@projections": workspaceProjectionCollection,
			"encodingKey": source.Key(), "constructionKey": tableShapeConstructionTarget.projectionKey(source)}, func(row map[string]any) error {
			value, err := db.DecodeMigrationJSON[explorer.WorkspaceProjection](row["projection"])
			if err != nil {
				return err
			}
			if _, err := value.Validate(source); err != nil {
				return err
			}
			complete, ok := row["constructionComplete"].(bool)
			if !ok {
				return fmt.Errorf("workspace projection migration state is invalid")
			}
			if complete {
				legacy, err := workspaceContainsTableShape(value.Workspace)
				if err != nil {
					return err
				}
				if legacy {
					return fmt.Errorf("immutable source is missing its Construction projection")
				}
			}
			projection = &value
			return nil
		})
	if err != nil {
		return explorer.WorkspaceProjection{}, fmt.Errorf("load immutable workspace projection: %w", err)
	}
	if projection == nil {
		return explorer.WorkspaceProjection{}, explorer.ErrNotFound
	}
	return *projection, nil
}

var _ explorer.WorkspaceProjectionReader = (*Store)(nil)
