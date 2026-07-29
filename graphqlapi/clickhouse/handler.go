package clickhouse

import (
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
)

func NewHandler(service MaterializationService) http.Handler {
	server := gqlhandler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: NewResolver(service),
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ctx, ok := fiberadaptor.LocalContextFromHTTPRequest(r); ok {
			r = r.WithContext(ctx)
		}
		server.ServeHTTP(w, r)
	})
}
