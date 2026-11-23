package explorerapi

import (
	"context"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/calypr/loom/internal/testkit/httpfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"
	"github.com/gofiber/fiber/v3"
)

type testHTTPResponse = httpfixtures.Response

var requestJSON = httpfixtures.RequestJSON

type testExplorerStore = explorerfixtures.Store

func newTestExplorerStore() *testExplorerStore {
	return explorerfixtures.NewStore()
}

var testExplorerKey = explorerfixtures.Key

type compilerTestRegistry = recipetest.Registry

func testAuthoringV2CapabilitySnapshot() capability.Snapshot {
	return capabilityfixtures.AuthoringV2CapabilitySnapshot(capabilityresolver.ScopeDigest(authscope.ReadScope{Mode: authscope.ReadScopeUnrestricted}))
}

func registerGeneratedExplorerTestRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, config lifecycle.Config) {
	handlers := NewHandlers(Config{Authorizer: authorizer, AuthorizeRead: authorizeRead, Explorers: service, Lifecycle: config})
	loomapi.RegisterHandlersWithOptions(router, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})
}

type HTTPRoutes struct {
	explorer *Handlers
}

func newExplorerHTTPHandlers(authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, configs ...lifecycle.Config) *Handlers {
	config := lifecycle.Config{}
	if len(configs) > 0 {
		config = configs[0]
	}
	return NewHandlers(Config{Authorizer: authorizer, AuthorizeRead: authorizeRead, Explorers: service, Lifecycle: config})
}

type uncalledStrictRoutes struct {
	loomapi.StrictServerInterface
}

type strictExplorerTestServer struct {
	*Handlers
	uncalledStrictRoutes
}

func strictExplorerHandlers(handlers *Handlers) loomapi.StrictServerInterface {
	return strictExplorerTestServer{Handlers: handlers}
}
