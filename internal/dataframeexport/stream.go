package dataframeexport

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/calypr/loom/internal/dataframe"
	"github.com/calypr/loom/internal/export"
)

var errRunnerRequired = errors.New("dataframe stream runner is required")

// Runner is the narrow dataframe execution boundary needed for export. It is
// satisfied by *dataframe.Service. Keeping it small lets callers retain the
// dataframe service's existing validation, authorization, and streaming
// behavior without coupling export code to service construction.
type Runner interface {
	Stream(context.Context, dataframe.RunRequest, func(map[string]any) error) (dataframe.StreamResult, error)
}

// NewRowStream creates an export.RowStream backed by runner for request. The
// request is passed unchanged to every invocation, and no rows are buffered or
// copied by this adapter. A dataframe.Service validates the request when each
// stream invocation begins.
//
// The returned stream preserves a runner error and a row-visitor error without
// wrapping either. It also checks cancellation before invoking the runner and
// after a successful invocation. This matters because inferred CSV executes
// the stream twice; callers need an externally stable dataset generation or
// snapshot for that mode, which Loom does not provide yet.
func NewRowStream(runner Runner, request dataframe.RunRequest) (export.RowStream, error) {
	if runner == nil {
		return nil, errRunnerRequired
	}

	return func(ctx context.Context, visit export.RowVisitor) error {
		if visit == nil {
			return fmt.Errorf("export row visitor is required")
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		_, err := runner.Stream(ctx, request, visit)
		if err != nil {
			return err
		}
		return ctx.Err()
	}, nil
}

// EncodeCSV writes CSV rows from request using the existing export encoder.
// Explicit options.Columns executes the dataframe request once. Omitting
// columns executes it twice (discovery, then writing), so it is safe only when
// an external caller guarantees a stable dataset generation or snapshot; Loom
// does not provide that contract yet.
func EncodeCSV(ctx context.Context, dst io.Writer, options export.CSVOptions, runner Runner, request dataframe.RunRequest) (export.Result, error) {
	stream, err := NewRowStream(runner, request)
	if err != nil {
		return export.Result{}, err
	}
	return export.EncodeCSV(ctx, dst, options, stream)
}
