package explorerapi

import (
	"context"

	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/explorer"
	"github.com/calypr/loom/internal/explorer/constructioninputs"
	"github.com/calypr/loom/internal/explorer/lifecycle"
	"github.com/calypr/loom/internal/explorer/viewbuilder"
)

type explorerConfigReadAuthorizer func(context.Context, *authscope.Principal, string) error

type Handlers struct {
	authorizer         authscope.Authorizer
	authorizeRead      explorerConfigReadAuthorizer
	views              *viewbuilder.Service
	application        *lifecycle.Service
	constructionInputs constructioninputs.Catalog
	authorizeExecution func(context.Context, string) error
}

type Config struct {
	Authorizer         authscope.Authorizer
	AuthorizeRead      func(context.Context, *authscope.Principal, string) error
	Explorers          *explorer.Service
	Lifecycle          lifecycle.Config
	ConstructionInputs constructioninputs.Catalog
	AuthorizeExecution func(context.Context, string) error
}

func NewHandlers(config Config) *Handlers {
	application, _ := lifecycle.New(config.Explorers, config.Lifecycle)
	return &Handlers{
		authorizer:         config.Authorizer,
		authorizeRead:      config.AuthorizeRead,
		views:              viewbuilder.New(config.Explorers),
		application:        application,
		constructionInputs: config.ConstructionInputs,
		authorizeExecution: config.AuthorizeExecution,
	}
}
