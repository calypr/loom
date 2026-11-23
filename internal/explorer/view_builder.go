package explorer

import (
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
