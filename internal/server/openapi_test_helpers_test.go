package server

import (
	"context"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

// registerGeneratedExplorerTestRoutes exercises the same generated strict
// adapter used in production; tests no longer maintain a second route tree.
func registerGeneratedExplorerTestRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, config ExplorerV2LifecycleConfig) {
	routes := &HTTPRoutes{explorer: newExplorerHTTPHandlers(authorizer, authorizeRead, service, config)}
	loomapi.RegisterHandlersWithOptions(router, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
}
