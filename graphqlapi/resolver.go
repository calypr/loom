package graphqlapi

import dataframeapi "github.com/calypr/loom/graphqlapi/dataframe"

type Resolver struct {
	service *dataframeapi.Service
}

type ResolverConfig = dataframeapi.Config

func NewResolver(cfg ResolverConfig) *Resolver {
	return &Resolver{service: dataframeapi.NewService(cfg)}
}
