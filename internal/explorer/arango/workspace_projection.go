package arango

import (
	"context"
	"fmt"

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
	err := s.client.QueryRows(ctx, `FOR migration IN @@ledger
FILTER migration._key == @version AND migration.version == @version
FOR projection IN @@projections FILTER projection._key == @key RETURN projection`, 1,
		map[string]any{"@ledger": workspaceMigrationLedger, "version": workspaceMigrationVersion,
			"@projections": workspaceProjectionCollection, "key": source.Key()}, func(row map[string]any) error {
			value, err := decode[explorer.WorkspaceProjection](row)
			if err != nil {
				return err
			}
			if _, err := value.Validate(source); err != nil {
				return err
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
