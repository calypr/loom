package server

import (
	"context"

	explorerapi "github.com/calypr/loom/internal/api/explorer"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/gofiber/fiber/v3"
)

func newExplorerHTTPHandlers(authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, configs ...lifecycle.Config) *explorerapi.Handlers {
	var config lifecycle.Config
	if len(configs) > 0 {
		config = configs[0]
	}
	return explorerapi.NewHandlers(explorerapi.Config{Authorizer: authorizer, AuthorizeRead: authorizeRead, Explorers: service, Lifecycle: config})
}

func registerGeneratedExplorerTestRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, config lifecycle.Config) {
	routeHandlers := &routes{explorerHandlers: newExplorerHTTPHandlers(authorizer, authorizeRead, service, config)}
	loomapi.RegisterHandlersWithOptions(router, loomapi.NewStrictHandler(routeHandlers, nil), loomapi.FiberServerOptions{})
}
