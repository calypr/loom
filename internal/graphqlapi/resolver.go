package graphqlapi

import "github.com/calypr/loom/internal/dataframebuilder"

type Resolver struct {
	service *dataframebuilder.Service
}

type ResolverConfig = dataframebuilder.Config

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{service: dataframebuilder.NewService(cfg)}
}
