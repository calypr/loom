package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

func (s *Service) BrowseSemanticInventory(ctx context.Context, request semanticbrowse.BrowseSemanticInventoryRequest) (semanticbrowse.BrowseSemanticInventoryResponse, error) {
	return s.browseService().BrowseSemanticInventory(ctx, request)
}
