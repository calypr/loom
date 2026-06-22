package graphqlapi

// Resolver wires gqlgen resolvers onto the dataframe builder service layer.
type Resolver struct {
	Service *Service
}

func NewResolver(service *Service) *Resolver {
	return &Resolver{Service: service}
}
