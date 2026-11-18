package server

import (
	loomapi "github.com/calypr/loom/api/loomapi"
	loadapi "github.com/calypr/loom/internal/api/bulk/load"
	httpapi "github.com/calypr/loom/internal/api/http"
	"github.com/calypr/loom/internal/authscope"
	"github.com/calypr/loom/internal/dataframe/publication"
)

type HTTPRoutes struct {
	server     *httpapi.HTTPServer
	load       *loadapi.Handler
	releases   publication.BundleCatalog
	scopes     *authscope.ScopeResolver
	explorer   *explorerHTTPHandlers
	dataframes dataframeReadRoutes
}

var _ loomapi.StrictServerInterface = (*HTTPRoutes)(nil)
