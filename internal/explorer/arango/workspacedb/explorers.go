package workspacedb

import (
	"context"
	"github.com/calypr/loom/internal/explorer/arango/db"

	"github.com/calypr/loom/internal/explorer"
)

func (s *WorkspaceStore) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	out := []explorer.Explorer{}
	err := s.client.QueryRows(ctx, db.MigrationJSONQuery(`FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, true), 1000, map[string]any{"@c": db.ExplorersCollection, "project": project}, func(row map[string]any) error {
		v, err := db.DecodeMigrationJSON[explorer.Explorer](row["migrationJSON"])
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	return out, err
}
func (s *WorkspaceStore) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	var out *explorer.Explorer
	err := s.client.QueryRows(ctx, db.MigrationJSONQuery(`FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, true), 1, map[string]any{"@c": db.ExplorersCollection, "key": db.ExplorerKey(project, id), "project": project}, func(row map[string]any) error {
		v, err := db.DecodeMigrationJSON[explorer.Explorer](row["migrationJSON"])
		out = &v
		return err
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
func (s *WorkspaceStore) Create(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	raw, err := db.Document(e, db.ExplorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": db.ExplorersCollection, "doc": raw}, func(row map[string]any) error { v, err := db.Decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}
