package execution

import (
	"context"
)

func (e *Engine) Streams(ctx context.Context, resolved Resolved) ([]OutputStream, error) {
	return e.streamExecutor.streamsWithLimit(ctx, resolved, 0)
}

func (e *Engine) Run(ctx context.Context, resolved Resolved) (map[string][]map[string]any, error) {
	return e.streamExecutor.run(ctx, resolved)
}
