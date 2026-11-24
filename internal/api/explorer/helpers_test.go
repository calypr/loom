package explorerapi

import (
	"context"
	"fmt"

	loomapi "github.com/calypr/loom/api/loomapi"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	explorerarango "github.com/calypr/loom/internal/explorer/arango"
	"github.com/calypr/loom/internal/explorer/arango/publicationdb"
	"github.com/calypr/loom/internal/explorer/arango/selectiondb"
	"github.com/calypr/loom/internal/explorer/arango/workspacedb"
	"github.com/calypr/loom/internal/explorer/capability"
	capabilityresolver "github.com/calypr/loom/internal/explorer/capability/resolver"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	storearango "github.com/calypr/loom/internal/store/arango"
	"github.com/calypr/loom/internal/testkit/capabilityfixtures"
	"github.com/calypr/loom/internal/testkit/explorerfixtures"
	"github.com/calypr/loom/internal/testkit/httpfixtures"
	"github.com/calypr/loom/internal/testkit/recipetest"
	"github.com/gofiber/fiber/v3"
)

type testHTTPResponse = httpfixtures.Response

var requestJSON = httpfixtures.RequestJSON

type testExplorerStore = explorerfixtures.Store

type testExplorerArangoStore struct {
	*explorerarango.DraftStore
	*selectiondb.SelectionStore
	*workspacedb.RevisionStore
	*workspacedb.WorkspaceStore
	*explorerarango.ExplicitGroupStore
	*publicationdb.Store
	*explorerarango.InterpretationStore
	*explorerarango.WorkspaceProjectionStore
}

func newTestExplorerArangoStore(client *storearango.Client) (*testExplorerArangoStore, error) {
	if client == nil {
		return nil, fmt.Errorf("Explorer Arango client is required")
	}
	selections := selectiondb.NewSelectionStore(client)
	publications, err := publicationdb.NewStore(client)
	if err != nil {
		return nil, err
	}
	return &testExplorerArangoStore{
		DraftStore: explorerarango.NewDraftStore(client), SelectionStore: selections,
		RevisionStore: workspacedb.NewRevisionStore(client), WorkspaceStore: workspacedb.NewWorkspaceStore(client),
		ExplicitGroupStore: explorerarango.NewExplicitGroupStore(client, selections),
		Store:              publications, InterpretationStore: explorerarango.NewInterpretationStore(client),
		WorkspaceProjectionStore: explorerarango.NewWorkspaceProjectionStore(client),
	}, nil
}

func newTestExplorerStore() *testExplorerStore {
	return explorerfixtures.NewStore()
}

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
