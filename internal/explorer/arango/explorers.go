package arango

import (
	"context"

	"github.com/calypr/loom/internal/explorer"
)

func (s *Store) List(ctx context.Context, project string) ([]explorer.Explorer, error) {
	out := []explorer.Explorer{}
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d.project == @project SORT d.explorerId RETURN d`, 1000, map[string]any{"@c": ExplorersCollection, "project": project}, func(row map[string]any) error {
		v, err := decode[explorer.Explorer](row)
		if err == nil {
			out = append(out, v)
		}
		return err
	})
	return out, err
}
func (s *Store) Get(ctx context.Context, project, id string) (*explorer.Explorer, error) {
	var out *explorer.Explorer
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key AND d.project == @project RETURN d`, 1, map[string]any{"@c": ExplorersCollection, "key": explorerKey(project, id), "project": project}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	return out, nil
}
func (s *Store) CreateInteractive(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	raw, err := document(e, explorerKey(e.Project, e.ExplorerID))
	if err != nil {
		return nil, err
	}
	var out *explorer.Explorer
	err = s.client.QueryRows(ctx, `INSERT @doc INTO @@c OPTIONS { overwriteMode: "ignore" } RETURN NEW`, 1, map[string]any{"@c": ExplorersCollection, "doc": raw}, func(row map[string]any) error { v, err := decode[explorer.Explorer](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrDraftConflict
	}
	return out, nil
}
func (s *Store) CreateRepository(ctx context.Context, e explorer.Explorer) (*explorer.Explorer, error) {
	return s.CreateInteractive(ctx, e)
}
