package server

import (
	"context"
	"fmt"
	"log/slog"

	loomapi "github.com/calypr/loom/api/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	explorerapi "github.com/calypr/loom/internal/api/explorer"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	publication "github.com/calypr/loom/internal/dataframe/publication"
	arangostore "github.com/calypr/loom/internal/store/arango"
	clickhousestore "github.com/calypr/loom/internal/store/clickhouse"
)

type explorerHandlers = explorerapi.Handlers

type routes struct {
	*api.HealthHandlers
	*loadapi.StrictHandlers
	*dataframeapi.Handlers
	*explorerHandlers
}

var _ loomapi.StrictServerInterface = (*routes)(nil)

func registerRoutes(server *api.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, explorerHandlers *explorerapi.Handlers, optional ...any) error {
	if explorerHandlers == nil {
		return fmt.Errorf("create Explorer HTTP routes: Explorer handlers are required")
	}
	var sourceLoader loadapi.SourceLoader
	var sourceLimits loadapi.SourceLoadLimits
	for _, value := range optional {
		switch typed := value.(type) {
		case loadapi.SourceLoader:
			sourceLoader = typed
		case loadapi.SourceLoadLimits:
			sourceLimits = typed
		}
	}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{Service: generationService, Authorizer: authorizer, SourceLoader: sourceLoader, SourceLoadLimits: sourceLimits})
	if err != nil {
		return fmt.Errorf("create generation load handler: %w", err)
	}
	var releases publication.BundleCatalog
	var scopes *authscope.ScopeResolver
	var dataframes dataframeReadRoutes
	for _, value := range optional {
		switch typed := value.(type) {
		case dataframeReadRoutes:
			dataframes = typed
		case publication.BundleCatalog:
			releases = typed
		case *authscope.ScopeResolver:
			scopes = typed
		}
	}
	handlers := &routes{
		HealthHandlers:   api.NewHealthHandlers(server),
		StrictHandlers:   loadapi.NewStrictHandlers(loadHandler),
		Handlers:         dataframeapi.NewHandlers(dataframes.published, dataframes.query, dataframeapi.NewRecipeExecutionReader(releases, scopes, nil)),
		explorerHandlers: explorerHandlers,
	}
	handler := loomapi.NewStrictHandler(handlers, nil)
	loomapi.RegisterHandlersWithOptions(server.App(), handler, loomapi.FiberServerOptions{})
	return nil
}

func wireHTTP(lifecycleClient *arangostore.Client, clickhouse *clickhousestore.Client, clickHouseEnabled bool, degradation error, authenticator authscope.Authenticator, authorizer authscope.Authorizer, logger *slog.Logger) (*api.HTTPServer, error) {
	server, err := api.NewHTTPServer(api.HTTPConfig{Authenticator: authenticator, Authorizer: authorizer, Logger: logger,
		CoreReadyCheck: func(ctx context.Context) error {
			return lifecycleClient.QueryRows(ctx, "RETURN {ready: true}", 1, nil, func(map[string]any) error { return nil })
		},
		ClickHouseReadyCheck: func(ctx context.Context) error {
			if degradation != nil {
				return degradation
			}
			if clickhouse == nil {
				return nil
			}
			return clickhouse.Ping(ctx)
		}, ClickHouseEnabled: clickHouseEnabled})
	if err != nil {
		return nil, fmt.Errorf("create HTTP server: %w", err)
	}
	return server, nil
}
