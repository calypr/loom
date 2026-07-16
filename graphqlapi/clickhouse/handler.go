package clickhouse

import (
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	"github.com/calypr/loom/graphqlapi/materialization"
)

func NewHandler(service *materializationapi.Service) http.Handler {
	server := gqlhandler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: NewResolver(service),
	}))
	return server
}
