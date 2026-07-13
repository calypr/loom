package arango

import (
	"context"
	"encoding/json"
)

type RowVisitor func(map[string]any) error

type CollectionSpec struct {
	Name     string
	Edge     bool
	Truncate bool
	Indexes  [][]string
}

type BootstrapSpec struct {
	Collections []CollectionSpec
	Reporter    func(event string, fields map[string]any)
}

type Backend interface {
	Bootstrap(ctx context.Context, spec BootstrapSpec) error
	InsertBatchRaw(ctx context.Context, collection string, docs []json.RawMessage, overwrite bool, writeAPI string) error
	QueryRows(ctx context.Context, query string, batchSize int, bindVars map[string]interface{}, visit RowVisitor) error
	Close(ctx context.Context) error
}
