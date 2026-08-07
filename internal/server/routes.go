package server

import (
	"fmt"

	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	graphapi "github.com/calypr/loom/internal/api/graphql/graph"
	"github.com/calypr/loom/internal/api/graphql/graph/resolver"
	api "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataset"
)

func registerRoutes(server *api.HTTPServer, resourceService *loadapi.Service, snapshotService *loadapi.SnapshotService, releaseService *dataset.ReleaseService, authorizer authscope.Authorizer, graphResolver *resolver.Resolver) error {
	resourceHandler, err := loadapi.NewHandler(loadapi.Config{Service: resourceService, Authorizer: authorizer, Snapshots: snapshotService, Releases: releaseService})
	if err != nil {
		return fmt.Errorf("create resource load handler: %w", err)
	}
	resourceHandler.RegisterResourceRoute(server.App())
	resourceHandler.RegisterSnapshotRoutes(server.App())
	graphapi.RegisterRoutes(server.App(), graphapi.RouteConfig{Handler: graphapi.NewHandler(graphResolver, server.Logger()), Playground: graphapi.NewPlaygroundHandler("/graphql/graph"), Sandbox: graphapi.NewApolloSandboxHandler("/graphql/graph")})
	return nil
}
