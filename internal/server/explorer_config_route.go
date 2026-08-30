package server

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/lifecycle"
)

type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

// explorerHTTPHandlers holds the dependencies used by the generated OpenAPI
// Explorer adapter. Fiber routing is registered once, from the generated
// server, rather than through a parallel handwritten handler tree.
type explorerHTTPHandlers struct {
	authorizer    authscope.Authorizer
	authorizeRead explorerConfigReadAuthorizer
	application   *lifecycle.Service
}

func newExplorerHTTPHandlers(authorizer authscope.Authorizer, authorizeRead explorerConfigReadAuthorizer, explorers *explorer.Service, configs ...lifecycle.Config) *explorerHTTPHandlers {
	config := lifecycle.Config{}
	if len(configs) > 0 {
		config = configs[0]
	}
	application, _ := lifecycle.New(explorers, config)
	return &explorerHTTPHandlers{
		authorizer:    authorizer,
		authorizeRead: authorizeRead,
		application:   application,
	}
}
