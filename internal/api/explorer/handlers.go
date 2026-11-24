package explorerapi

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/projectconfig"
)

type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

type Handlers struct {
	authorizer         authscope.Authorizer
	authorizeRead      explorerConfigReadAuthorizer
	views              *projectconfig.Service
	application        *lifecycle.Service
	owners             lifecycle.OwnerServices
	constructionInputs constructioninputs.Catalog
	authorizeExecution func(context.Context, string) error
}

type Config struct {
	Authorizer         authscope.Authorizer
	AuthorizeRead      func(context.Context, *authscope.Principal, string) error
	Explorers          *explorer.Service
	Application        *lifecycle.Service
	Revisions          explorer.RevisionReader
	ViewBuilder        explorer.ViewBuilderRepository
	ConstructionInputs constructioninputs.Catalog
	AuthorizeExecution func(context.Context, string) error
}

func NewHandlers(config Config) *Handlers {
	handlers := &Handlers{
		authorizer:         config.Authorizer,
		authorizeRead:      config.AuthorizeRead,
		views:              projectconfig.New(config.Explorers, config.Revisions, config.ViewBuilder),
		application:        config.Application,
		constructionInputs: config.ConstructionInputs,
		authorizeExecution: config.AuthorizeExecution,
	}
	if config.Application != nil {
		handlers.owners = config.Application.OwnerServices()
	}
	return handlers
}
