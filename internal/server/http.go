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

type routeDependencies struct {
	sourceLoader loadapi.SourceLoader
	sourceLimits loadapi.SourceLoadLimits
	releases     publication.BundleCatalog
	scopes       *authscope.ScopeResolver
	dataframes   dataframeReadRoutes
}

func registerRoutes(server *api.HTTPServer, generationService *loadapi.Service, authorizer authscope.Authorizer, explorerHandlers *explorerapi.Handlers, deps routeDependencies) error {
	if explorerHandlers == nil {
		return fmt.Errorf("create Explorer HTTP routes: Explorer handlers are required")
	}
	loadHandler, err := loadapi.NewHandler(loadapi.Config{Service: generationService, Authorizer: authorizer, SourceLoader: deps.sourceLoader, SourceLoadLimits: deps.sourceLimits})
	if err != nil {
		return fmt.Errorf("create generation load handler: %w", err)
	}
	handlers := &routes{
		HealthHandlers:   api.NewHealthHandlers(server),
		StrictHandlers:   loadapi.NewStrictHandlers(loadHandler),
		Handlers:         dataframeapi.NewHandlers(deps.dataframes.published, deps.dataframes.query, dataframeapi.NewRecipeExecutionReader(deps.releases, deps.scopes, nil)),
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
