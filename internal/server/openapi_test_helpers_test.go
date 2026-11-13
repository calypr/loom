package server

import (
	"context"

	loomapi "github.com/calypr/loom/generated/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func registerGeneratedExplorerTestRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, config lifecycle.Config) {
	routes := &HTTPRoutes{explorer: newExplorerHTTPHandlers(authorizer, authorizeRead, service, config)}
	loomapi.RegisterHandlersWithOptions(router, loomapi.NewStrictHandler(routes, nil), loomapi.FiberServerOptions{})
}
