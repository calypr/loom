package proposal

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/receiptmodel"
	"github.com/calypr/loom/internal/explorer/viewer"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
)

type PreviewResult struct {
	Receipt *receiptmodel.CompilationReceipt
	Columns []viewer.EmittedColumn
	Summary dataframeexecution.PreviewSummary
}

type PreviewSinkFactory func(*receiptmodel.CompilationReceipt, []viewer.EmittedColumn) (func(map[string]any) error, error)

type ProposalPreviewOptions struct {
	Limit                 *int
	Budget                func(context.Context) (context.Context, time.Duration)
	Timeout               time.Duration
	AllowSampledAggregate bool
	Authorize             func(context.Context) error
	Sink                  PreviewSinkFactory
}

type ProposalPreview struct {
	Result    PreviewResult
	Started   time.Time
	Completed bool
}

type ProposalPreviewExecutionError struct{ Cause error }

func (e *ProposalPreviewExecutionError) Error() string { return e.Cause.Error() }
func (e *ProposalPreviewExecutionError) Unwrap() error { return e.Cause }
