package execution

import (
	"context"
)

func (e *Engine) outputStreams() streamExecutor {
	return streamExecutor{
		queryRows:                 e.queryRows,
		batchSize:                 e.batchSize,
		rootPageRows:              e.rootPageRows,
		clickHouseStreamForOutput: e.clickHouseStreamForOutput,
	}
}

func (e *Engine) Streams(ctx context.Context, resolved Resolved) ([]OutputStream, error) {
	return e.outputStreams().streamsWithLimit(ctx, resolved, 0)
}

func (e *Engine) Run(ctx context.Context, resolved Resolved) (map[string][]map[string]any, error) {
	return e.outputStreams().run(ctx, resolved)
}
