package explorer

import (
	"context"

	"github.com/calypr/loom/internal/explorer/viewer"
)

type ViewBuilderMutation struct {
	Project            string
	ExpectedRevisionID string
	CatalogRevisionID  string
	Publication        ProjectConfigPublication
	Actor              string
}

type ViewBuilderRepository interface {
	GetViewBuilderSettings(context.Context, string) (viewer.ViewBuilderSettings, error)
	SaveViewBuilderConfig(context.Context, ViewBuilderMutation) error
	DeleteViewBuilderConfig(context.Context, string, string, string) error
	SaveViewBuilderDefaults(context.Context, string, viewer.ViewBuilderDefaultsRequest) error
	AdoptViewBuilderCatalogs(context.Context, string) error
}
