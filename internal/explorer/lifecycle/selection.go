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

type SelectionSourceIntent = selection.SelectionSourceIntent
type PublishedOutputIntent = selection.PublishedOutputIntent
type SelectionIntentCreateRequest = selection.SelectionIntentCreateRequest
type SelectionCreateResult = selection.SelectionCreateResult
type SelectionReadIntentRequest = selection.SelectionReadIntentRequest
type SelectionReadResult = selection.SelectionReadResult
type SelectionSourceResolver = selection.SelectionSourceResolver
type SelectionReferenceValidator = selection.SelectionReferenceValidator

const (
	SelectionSourceResources         = selection.SelectionSourceResources
	SelectionSourcePublishedOutput   = selection.SelectionSourcePublishedOutput
	SelectionSourceSelectionRevision = selection.SelectionSourceSelectionRevision
)

func (s *Service) CreateSelection(ctx context.Context, req SelectionIntentCreateRequest) (SelectionCreateResult, error) {
	return s.selectionService.CreateSelection(ctx, req)
}

func (s *Service) ReadSelection(ctx context.Context, req SelectionReadIntentRequest) (SelectionReadResult, error) {
	return s.selectionService.ReadSelection(ctx, req)
}
