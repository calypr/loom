package clickhouse

import (
	"context"
	"net/http"

	gqlhandler "github.com/99designs/gqlgen/graphql/handler"
	dataframeerrors "github.com/calypr/loom/internal/dataframe/errors"
	fiberadaptor "github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/vektah/gqlparser/v2/gqlerror"
)

func NewHandler(service MaterializationService) http.Handler {
	server := gqlhandler.NewDefaultServer(NewExecutableSchema(Config{
		Resolvers: NewResolver(service),
	}))
	server.SetErrorPresenter(func(ctx context.Context, err error) *gqlerror.Error {
		requestID, _ := ctx.Value("loom.graphql.request_id").(string)
		if gqlErr, ok := err.(*gqlerror.Error); ok && gqlErr != nil && gqlErr.Err == nil {
			copy := *gqlErr
			copy.Extensions = map[string]any{"code": "GRAPHQL_VALIDATION_FAILED", "retryable": false}
			if requestID != "" {
				copy.Extensions["requestId"] = requestID
			}
			return &copy
		}
		if gqlErr, ok := err.(*gqlerror.Error); ok && gqlErr != nil && gqlErr.Err != nil {
			normalized := dataframeerrors.Normalize(gqlErr.Err)
			result := &gqlerror.Error{Err: gqlErr.Err, Message: dataframeerrors.PublicMessage(normalized), Extensions: map[string]any{"code": normalized.Code(), "retryable": normalized.Retryable()}, Path: gqlErr.Path, Locations: gqlErr.Locations}
			if requestID != "" {
				result.Extensions["requestId"] = requestID
			}
			return result
		}
		normalized := dataframeerrors.Normalize(err)
		result := &gqlerror.Error{Err: err, Message: dataframeerrors.PublicMessage(normalized), Extensions: map[string]any{"code": normalized.Code(), "retryable": normalized.Retryable()}}
		if requestID != "" {
			result.Extensions["requestId"] = requestID
		}
		if gqlErr, ok := err.(*gqlerror.Error); ok {
			result.Path, result.Locations = gqlErr.Path, gqlErr.Locations
		}
		return result
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
