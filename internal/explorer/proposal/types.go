package proposal

import (
	"context"
	"time"

	"github.com/calypr/loom/internal/explorer/authoringcore"
)

type ProposalPreviewOptions struct {
	Limit                 *int
	Budget                func(context.Context) (context.Context, time.Duration)
	Timeout               time.Duration
	AllowSampledAggregate bool
	Authorize             func(context.Context) error
	Sink                  authoringcore.PreviewSinkFactory
}

type ProposalPreview struct {
	Result    authoringcore.PreviewResult
	Started   time.Time
	Completed bool
}

type ProposalPreviewExecutionError struct{ Cause error }

func (e *ProposalPreviewExecutionError) Error() string { return e.Cause.Error() }
func (e *ProposalPreviewExecutionError) Unwrap() error { return e.Cause }
