package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

func (s *Service) ColumnSource(ctx context.Context, request semanticbrowse.ColumnSourceRequest) (semanticbrowse.ColumnSourceResponse, error) {
	return s.browseService().ColumnSource(ctx, request)
}
