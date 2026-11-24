package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/semanticbrowse"
)

type SearchSchemaFieldsRequest = semanticbrowse.SearchSchemaFieldsRequest
type SchemaFieldOption = semanticbrowse.SchemaFieldOption
type SchemaFieldSearchResponse = semanticbrowse.SchemaFieldSearchResponse

func (s *Service) SearchSchemaFields(ctx context.Context, request SearchSchemaFieldsRequest) (SchemaFieldSearchResponse, error) {
	return s.browseService().SearchSchemaFields(ctx, request)
}
