package explorer

import (
	"context"

	"github.com/calypr/loom/internal/explorer/viewer"
)

var ErrInvalidViewBuilder = viewer.ErrInvalidViewBuilder

type ViewBuilderSettings = viewer.ViewBuilderSettings
type ViewBuilderConfig = viewer.ViewBuilderConfig
type ViewBuilderCatalog = viewer.ViewBuilderCatalog
type ViewBuilderFrame = viewer.ViewBuilderFrame
type ViewBuilderColumn = viewer.ViewBuilderColumn
type ViewBuilderState = viewer.ViewBuilderState
type ViewBuilderSaveRequest = viewer.ViewBuilderSaveRequest
type ViewBuilderDefaultsRequest = viewer.ViewBuilderDefaultsRequest

var ViewBuilderFrameID = viewer.ViewBuilderFrameID

type ViewBuilderMutation struct {
	Project            string
	ExpectedRevisionID string
	CatalogRevisionID  string
	Publication        ProjectConfigPublication
	Actor              string
}

type ViewBuilderRepository interface {
	GetViewBuilderSettings(context.Context, string) (ViewBuilderSettings, error)
	SaveViewBuilderConfig(context.Context, ViewBuilderMutation) error
	DeleteViewBuilderConfig(context.Context, string, string, string) error
	SaveViewBuilderDefaults(context.Context, string, ViewBuilderDefaultsRequest) error
	AdoptViewBuilderCatalogs(context.Context, string) error
}
