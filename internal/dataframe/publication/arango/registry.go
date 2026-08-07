package arango

import (
	"context"
	"encoding/json"
	"fmt"

	arangostore "github.com/calypr/loom/internal/store/arango"
)

type Client interface {
	QueryRows(context.Context, string, int, map[string]interface{}, arangostore.RowVisitor) error
	InsertBatchRaw(context.Context, string, []json.RawMessage, bool, string) error
}

type Registry struct {
	client    Client
	batchSize int
}

func New(client Client) (*Registry, error) {
	if client == nil {
		return nil, fmt.Errorf("Arango bundle registry client is required")
	}
	return &Registry{client: client, batchSize: 32}, nil
}

func BootstrapSpec() arangostore.BootstrapSpec {
	return arangostore.BootstrapSpec{Collections: []arangostore.CollectionSpec{{
		Name:    BundleExecutionsCollection,
		Indexes: [][]string{{"key"}, {"state"}, {"project", "datasetGeneration", "name", "state"}},
	}, {
		Name:    BundlePointersCollection,
		Indexes: [][]string{{"executionId"}},
	}, {
		Name:    BundleLeasesCollection,
		Indexes: [][]string{{"expiresAt"}},
	}}}
}
