package server

import (
	loomapi "github.com/calypr/loom/api/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	dataframeapi "github.com/calypr/loom/internal/api/dataframe/dataframe"
	explorerapi "github.com/calypr/loom/internal/api/explorer"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

type explorerHandlers = explorerapi.Handlers

type HTTPRoutes struct {
	*httpapi.HealthHandlers
	*loadapi.StrictHandlers
	*dataframeapi.Handlers
	server   *httpapi.HTTPServer
	load     *loadapi.Handler
	releases publication.BundleCatalog
	scopes   *authscope.ScopeResolver

	*explorerHandlers
	dataframes dataframeReadRoutes
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)
