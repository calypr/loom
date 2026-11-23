package population

import "github.com/calypr/loom/internal/explorer/capability"

type PopulationRoutesRequest struct {
	Project             string
	ExplorerID          string
	SnapshotToken       string
	OutputID            string
	SelectionRevisionID string
	Limit               int
	Cursor              string
}

type PopulationRouteChoice struct {
	RouteChoiceID string                                    `json:"routeChoiceId"`
	Route         []capability.ConstructionRouteStep        `json:"route"`
	Presentation  capability.ConstructionChoicePresentation `json:"presentation"`
}

type PopulationRoutesResponse struct {
	SnapshotToken       string                  `json:"snapshotToken"`
	OutputID            string                  `json:"outputId"`
	SelectionRevisionID string                  `json:"selectionRevisionId"`
	Complete            bool                    `json:"complete"`
	Truncated           bool                    `json:"truncated"`
	NextCursor          string                  `json:"nextCursor,omitempty"`
	Choices             []PopulationRouteChoice `json:"choices"`
}
