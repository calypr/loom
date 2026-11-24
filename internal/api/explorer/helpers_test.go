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

func registerGeneratedExplorerTestRoutes(router fiber.Router, authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, config lifecycle.Config, backing ...explorer.ExplorerStore) {
	dependencies := explorerDependenciesForTest(backing...)
	config = lifecycleConfigForTest(config, dependencies)
	handlers := NewHandlers(Config{Authorizer: authorizer, AuthorizeRead: authorizeRead, Explorers: service, Revisions: dependencies.Revisions, ViewBuilder: dependencies.ViewBuilder, Lifecycle: config})
	loomapi.RegisterHandlersWithOptions(router, loomapi.NewStrictHandler(strictExplorerHandlers(handlers), nil), loomapi.FiberServerOptions{})
}

type HTTPRoutes struct {
	explorer *Handlers
}

func newExplorerHTTPHandlers(authorizer authscope.Authorizer, authorizeRead func(context.Context, *authscope.Principal, string) error, service *explorer.Service, options ...any) *Handlers {
	config := lifecycle.Config{}
	var backing []explorer.ExplorerStore
	for _, option := range options {
		switch value := option.(type) {
		case lifecycle.Config:
			config = value
		case explorer.ExplorerStore:
			backing = append(backing, value)
		}
	}
	dependencies := explorerDependenciesForTest(backing...)
	config = lifecycleConfigForTest(config, dependencies)
	return NewHandlers(Config{Authorizer: authorizer, AuthorizeRead: authorizeRead, Explorers: service, Revisions: dependencies.Revisions, ViewBuilder: dependencies.ViewBuilder, Lifecycle: config})
}

func explorerDependenciesForTest(backing ...explorer.ExplorerStore) explorerfixtures.Stores {
	store := explorer.ExplorerStore(explorerfixtures.NewStore())
	if len(backing) > 0 {
		store = backing[0]
	}
	return explorerfixtures.Dependencies(store)
}

func lifecycleConfigForTest(config lifecycle.Config, dependencies explorerfixtures.Stores) lifecycle.Config {
	if config.ExplorerOwners == nil {
		config.ExplorerOwners = dependencies.Owners
	}
	if config.DraftStore == nil {
		config.DraftStore = dependencies.Drafts
	}
	if config.DraftRevisions == nil {
		config.DraftRevisions = dependencies.DraftRevisions
	}
	if config.Revisions == nil {
		config.Revisions = dependencies.Revisions
	}
	if config.RevisionWriter == nil {
		config.RevisionWriter = dependencies.RevisionWriter
	}
	if config.Receipts == nil {
		config.Receipts = dependencies.Receipts
	}
	if config.AuthoringPublisher == nil {
		config.AuthoringPublisher = dependencies.AuthoringPublisher
	}
	if config.RepositoryActivator == nil {
		config.RepositoryActivator = dependencies.RepositoryActivator
	}
	if config.SelectionStore == nil {
		config.SelectionStore = dependencies.Selections
	}
	return config
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
