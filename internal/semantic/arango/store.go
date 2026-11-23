package arango

import (
	"context"
	"fmt"

	store "github.com/calypr/loom/internal/store/arango"
)

type client interface {
	store.RowQueryer
	store.BatchInserter
	store.AQLExecutor
	CollectionExists(context.Context, string) (bool, error)
	Bootstrap(context.Context, store.BootstrapSpec) error
}

type SemanticInventoryStore struct{ client client }

type SemanticCoverageStore struct {
	client    client
	inventory SemanticInventoryStore
}

type Store struct {
	SemanticInventoryStore
	SemanticCoverageStore
}

func New(c client) (*Store, error) {
	if c == nil {
		return nil, fmt.Errorf("semantic Arango client is required")
	}
	inventory := SemanticInventoryStore{client: c}
	return &Store{
		SemanticInventoryStore: inventory,
		SemanticCoverageStore:  SemanticCoverageStore{client: c, inventory: inventory},
	}, nil
}
