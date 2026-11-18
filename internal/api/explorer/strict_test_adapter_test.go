package explorerapi

import loomapi "github.com/calypr/loom/api/loomapi"

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
