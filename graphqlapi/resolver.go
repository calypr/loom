package graphqlapi

import (
	materializationapi "github.com/calypr/loom/graphqlapi/materialization"
	queryapi "github.com/calypr/loom/graphqlapi/query"
	"github.com/calypr/loom/internal/dataframe/materialization"
)

type Resolver struct {
	query            *queryapi.Service
	materializations *materializationapi.Service
}

type ResolverConfig struct {
	DataframeQuery        queryapi.Config
	MaterializationReader *materialization.Reader
}

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{
		query: queryapi.NewService(cfg.DataframeQuery),
		materializations: materializationapi.NewService(materializationapi.Config{
			Reader:        cfg.MaterializationReader,
			ScopeResolver: cfg.DataframeQuery.ScopeResolver,
		}),
	}
}
