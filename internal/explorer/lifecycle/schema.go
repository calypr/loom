package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

func (s *Service) SearchSchemaFields(ctx context.Context, request semanticbrowse.SearchSchemaFieldsRequest) (semanticbrowse.SchemaFieldSearchResponse, error) {
	return s.browseService().SearchSchemaFields(ctx, request)
}
