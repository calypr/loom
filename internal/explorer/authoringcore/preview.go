package authoringcore

import (
	"github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"
)

type PreviewResult struct {
	Receipt *receiptmodel.CompilationReceipt
	Columns []viewer.EmittedColumn
	Summary execution.PreviewSummary
}

type PreviewSinkFactory func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error)
