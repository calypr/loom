package dump

import (
	"context"
	"io"

	dfmaterialization "github.com/calypr/loom/internal/dataframe/published"
)

type DataframeExporter interface {
	ExportDataframe(context.Context, dfmaterialization.ExportRequest, io.Writer) error
}
