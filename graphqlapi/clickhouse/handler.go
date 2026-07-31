package clickhouse

import (
	"context"
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	graphqlapi "github.com/calypr/loom/graphqlapi"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func NewHandler(service MaterializationService) http.Handler {
	server := gqlhandler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: NewResolver(service),
	}))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		requestID, _ := ctx.Value("loom.graphql.request_id").(string)
		return graphqlapi.PresentGraphQLError(err, requestID)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ctx, ok := fiberadaptor.LocalContextFromHTTPRequest(r); ok {
			r = r.WithContext(ctx)
		}
		if requestID, _ := r.Context().Value("loom.graphql.request_id").(string); r.Header.Get("X-Request-ID") == "" && requestID != "" {
			r.Header.Set("X-Request-ID", requestID)
		}
		r = r.WithContext(context.WithValue(r.Context(), "loom.graphql.request_id", r.Header.Get("X-Request-ID")))
		server.ServeHTTP(w, r)
	})
}
