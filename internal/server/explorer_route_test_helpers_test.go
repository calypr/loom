package server

import (
	graphresolver "github.com/calypr/loom/internal/api/graphql/graph/resolver"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/gofiber/fiber/v3"
)

func registerTestExplorerLifecycleRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, service *explorer.Service, config ExplorerV2LifecycleConfig) {
	handlers := newExplorerLifecycleHandlers(authorizer, authorizeRead, service, config)
	router.Get("/api/v1/projects/:project/explorers", handlers.list)
	router.Post("/api/v1/projects/:project/explorers", handlers.create)
	router.Get("/api/v1/projects/:project/explorers/:explorerId", handlers.get)
}

func registerTestExplorerAuthoringRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, service *explorer.Service, config ExplorerV2LifecycleConfig) {
	h := newExplorerAuthoringHandlers(authorizer, authorizeRead, service, config)
	base := "/api/v1/projects/:project/explorers/:explorerId/authoring/v2"
	router.Get(base+"/capability", h.getCapability)
	router.Post(base+"/suggestions", h.searchSuggestions)
	router.Get(base+"/builder", h.getBuilder)
	router.Post(base+"/builder", h.compileBuilder)
	router.Post(base+"/commands", h.applyCommands)
	router.Post(base+"/reconcile", h.reconcile)
	router.Post(base+"/preview", h.preview)
	router.Post(base+"/publish", h.publish)
}

func registerTestExplorerRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, service *explorer.Service, materialize graphresolver.ExplorerBundleMaterializer, config ExplorerV2LifecycleConfig) {
	h := newExplorerHTTPHandlers(authorizer, authorizeRead, service, materialize, config)
	router.Post("/api/v1/projects/:project/generations/:generation/explorer-config", h.publishRepositoryConfig)
	registerTestExplorerLifecycleRoutes(router, authorizer, authorizeRead, service, config)
	registerTestExplorerAuthoringRoutes(router, authorizer, authorizeRead, service, config)
}
