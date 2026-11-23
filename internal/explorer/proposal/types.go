package proposal

import (
	"context"
	"time"

	dataframeexecution "github.com/calypr/loom/internal/dataframe/execution"
	"github.com/calypr/loom/internal/explorer"
)

type PreviewResult struct {
	Receipt *explorer.CompilationReceipt
	Columns []explorer.EmittedColumn
	Summary dataframeexecution.PreviewSummary
}

type PreviewSinkFactory func(*explorer.CompilationReceipt, []explorer.EmittedColumn) (func(map[string]any) error, error)

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
