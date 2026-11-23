package explorer

import (
	"context"
)

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
