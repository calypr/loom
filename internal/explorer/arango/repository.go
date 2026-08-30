package arango

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer"
)

func repositoryConfigKey(project string) string {
	return "repository_config_" + key(project, "default")
}
func (s *Store) GetRepositoryConfig(ctx context.Context, project string) (*explorer.RepositoryConfig, error) {
	var out *explorer.RepositoryConfig
	err := s.client.QueryRows(ctx, `FOR d IN @@c FILTER d._key == @key RETURN d`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": repositoryConfigKey(project)}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, explorer.ErrNotFound
	}
	if out.ExplorerID == "" {
		out.ExplorerID, out.Management = "default", explorer.ManagementRepository
	}
	return out, nil
}
func (s *Store) SaveRepositoryConfig(ctx context.Context, value explorer.RepositoryConfig) (*explorer.RepositoryConfig, error) {
	value.ExplorerID, value.Management = "default", explorer.ManagementRepository
	value.UpdatedAt = time.Now().UTC()
	doc, err := document(value, repositoryConfigKey(value.Project))
	if err != nil {
		return nil, err
	}
	var out *explorer.RepositoryConfig
	err = s.client.QueryRows(ctx, `UPSERT { _key: @key } INSERT @doc UPDATE @doc IN @@c RETURN NEW`, 1, map[string]any{"@c": RepositoryConfigsCollection, "key": repositoryConfigKey(value.Project), "doc": doc}, func(row map[string]any) error { v, err := decode[explorer.RepositoryConfig](row); out = &v; return err })
	if err != nil {
		return nil, err
	}
	return out, nil
}
