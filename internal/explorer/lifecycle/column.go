package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

type ColumnSourceRequest = semanticbrowse.ColumnSourceRequest
type ColumnSourceRouteStep = semanticbrowse.ColumnSourceRouteStep
type ColumnSourceResponse = semanticbrowse.ColumnSourceResponse

func (s *Service) ColumnSource(ctx context.Context, request ColumnSourceRequest) (ColumnSourceResponse, error) {
	return s.browseService().ColumnSource(ctx, request)
}
