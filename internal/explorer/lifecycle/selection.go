package lifecycle

import (
	"context"

	"github.com/calypr/loom/internal/explorer/selection"
)

const (
	DefaultSelectionMaxRows     = selection.DefaultSelectionMaxRows
	DefaultSelectionMaxBytes    = selection.DefaultSelectionMaxBytes
	DefaultSelectionMaxDuration = selection.DefaultSelectionMaxDuration
)

const (
	SelectionSourceResources         = selection.SelectionSourceResources
	SelectionSourcePublishedOutput   = selection.SelectionSourcePublishedOutput
	SelectionSourceSelectionRevision = selection.SelectionSourceSelectionRevision
)

func (s *Service) CreateSelection(ctx context.Context, req selection.SelectionIntentCreateRequest) (selection.SelectionCreateResult, error) {
	return s.selectionService.CreateSelection(ctx, req)
}

func (s *Service) ReadSelection(ctx context.Context, req selection.SelectionReadIntentRequest) (selection.SelectionReadResult, error) {
	return s.selectionService.ReadSelection(ctx, req)
}
